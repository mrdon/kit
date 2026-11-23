/* The race scene: layouts (where the spots sit) x themes (the art), the
   specials, and the layout math the frogs are placed with. */
// ---------- track: layout x theme ----------
// A race is drawn by two independent, swappable pieces, picked by the snapshot:
//   LAYOUTS[S.layout]: where each spot sits on screen, which way a frog there faces, how high a
//                      stack there may grow, and the path line (geometry only, no art)
//   THEMES[S.theme]:   the art: background (or a photo), optional bank between tiers, the path,
//                      plain spots, start and finish. Any theme works on any layout.
// Everything is in 1920x1080 design pixels; fitStage() scales the #track layer to the screen.
// kinds come from the shared HOP constants (consts.js, mirrors engine/)
const LAYOUT = { STRAIGHT: HOP.layout.straight, SWITCHBACK: HOP.layout.switchback };
const SPOT = { LAUNCH: HOP.spot.launch, CHUTE: HOP.spot.chute };
const THEME = { BAR: "bar", LAKE: "lake" };
const DW = 1920, DH = 1080;
const FOOT = 0.06;
const SKY = 105; // nothing flies higher than this (design px from the top): the header lives above it // the frog art's feet sit this far (of its height) above the element's bottom edge

// pts[i] = { x, y (ground), face: 1 right / -1 left, ceil: lowest y a stack top may reach here }
const LAYOUTS = {
  // One row across the screen, START on the left, finish on the right (the old TV).
  [LAYOUT.STRAIGHT]: {
    geometry(n) {
      const y = 790, x0 = 170, xF = 1700, step = (xF - x0) / n;
      const fw = Math.max(90, Math.min(165, step * 1.1));
      const pts = Array.from({ length: n + 1 }, (_, i) => ({ x: x0 + i * step, y, face: 1, ceil: 96 }));
      return { pts, step, fw, shelfY: 380, d: `M${x0 - 110} ${y} H${xF + 110}` };
    },
  },
  // Two tiers: first half left->right along the bottom, round the bend on the right, back
  // right->left along an upper ledge to a finish above the start.
  [LAYOUT.SWITCHBACK]: {
    geometry(n) {
      const yLo = 800, yHi = 410, xR = 1600, rx = 190, x0 = 205, xF = 235;
      const ry = (yLo - yHi) / 2, cy = (yLo + yHi) / 2;
      const turn = [{ x: xR, y: yLo, s: 0 }];
      for (let k = 1; k <= 180; k++) {
        const th = Math.PI / 2 - (Math.PI * k) / 180, x = xR + rx * Math.cos(th), y = cy + ry * Math.sin(th), q = turn[k - 1];
        turn.push({ x, y, s: q.s + Math.hypot(x - q.x, y - q.y) });
      }
      const a = xR - x0, c = turn[turn.length - 1].s, total = a + c + (xR - xF);
      const at = s => {
        if (s <= a) return { x: x0 + s, y: yLo, dx: 1 };
        if (s >= a + c) return { x: xR - (s - a - c), y: yHi, dx: -1 };
        const u = s - a; let k = 1;
        while (k < turn.length - 1 && turn[k].s < u) k++;
        const p = turn[k - 1], q = turn[k], f = (u - p.s) / (q.s - p.s || 1);
        return { x: p.x + (q.x - p.x) * f, y: p.y + (q.y - p.y) * f, dx: q.x - p.x };
      };
      const step = total / n, fw = Math.max(90, Math.min(160, step * 0.95));
      const pts = Array.from({ length: n + 1 }, (_, i) => {
        const p = at(i * step), lower = p.y > cy + 1;
        // stacks on the lower half stop short of the ledge's name tags; the ledge's go up to the header
        return { x: p.x, y: p.y, face: p.dx > 0.01 || (Math.abs(p.dx) <= 0.01 && lower) ? 1 : -1, ceil: lower ? yHi + 72 : 92 };
      });
      return { pts, step, fw, shelfY: yHi - 172, tiers: { yLo, yHi, xR, rx, ry, cy },
        d: `M${x0 - 90} ${yLo} H${xR} A${rx} ${ry} 0 0 0 ${xR} ${yHi} H${xF - 90}` };
    },
  },
};

// ---------- theme art ----------
const FONT = `font-family="bungee, system-ui, sans-serif"`;
function tagSVG(x, y, text, fill, ink, stroke, fs = 26) {
  const w = text.length * fs * 0.86 + 28; // Bungee is wide
  return `<g transform="translate(${x} ${y})"><rect x="${-w / 2}" y="${-fs * 0.75}" width="${w}" height="${fs * 1.45}" rx="9" fill="${fill}" stroke="${stroke}" stroke-width="3"/>
    <text y="${fs * 0.42}" font-size="${fs}" text-anchor="middle" fill="${ink}" ${FONT}>${text}</text></g>`;
}
// START sign sits behind the start spot (against the direction of travel), clear of the frogs on it
const startTag = (p, G) => tagSVG(p.x - p.face * Math.max(115, G.fw * 0.85), p.y - 100, "START", "#fffbe8", "#2f5e8a", "#4a3219");
const SCENE_DEFS = `
  <filter id="rough"><feTurbulence type="fractalNoise" baseFrequency="0.9" numOctaves="2" seed="4"/>
    <feColorMatrix values="0 0 0 0 0.25  0 0 0 0 0.16  0 0 0 0 0.07  0 0 0 0.55 -0.2"/><feComposite in2="SourceGraphic" operator="in"/></filter>`;

// Defaults any theme can lean on. A photo theme (a place in town, side-view like a fighting-game
// stage) only needs { id, image: "<url of the photo>" }: it gets the dirt path, hollows and the brewery.
const THEME_BASE = {
  ambient: false,       // bar string lights
  image: null,          // full-screen background photo, drawn instead of background()
  background: () => `<rect width="${DW}" height="${DH}" fill="#6f8a55"/>`,
  bank: null,           // optional art for the face between tiers (G.tiers)
  path(G) {
    const w = 96 * (G.fw / 160);
    return `<path d="${G.d}" stroke="#6e5232" stroke-width="${w + 16}" fill="none" stroke-linecap="round" stroke-linejoin="round" opacity="0.55"/>
      <path d="${G.d}" stroke="#c49a66" stroke-width="${w}" fill="none" stroke-linecap="round" stroke-linejoin="round"/>
      <path d="${G.d}" stroke="#000" stroke-width="${w}" fill="none" stroke-linecap="round" stroke-linejoin="round" filter="url(#rough)" opacity="0.6"/>`;
  },
  spot(p, G) {
    const k = G.fw / 160, rx = 46 * k, ry = 17 * k;
    return `<ellipse cx="${p.x}" cy="${p.y}" rx="${rx}" ry="${ry}" fill="#8b6339"/>
      <ellipse cx="${p.x}" cy="${p.y - ry * 0.18}" rx="${rx * 0.86}" ry="${ry * 0.72}" fill="#a87b4a"/>`;
  },
  start(p, G) {
    return this.spot(p, G) + `<g transform="translate(${p.x + p.face * 70 * (G.fw / 160)} ${p.y})"><rect x="-6" y="-48" width="12" height="96" fill="#fffbe8" opacity="0.9"/></g>` + startTag(p, G);
  },
  finish(p, G) { return this.spot(p, G); }, // the spot under the finish frog; the brewery stands behind it
  breweryBase: "slab",  // brewerySVG base: "slab" (concrete step) or "dock" (planks, for water)
};

// The finish on every theme and layout: Gravity Brewing, the winner hops into its doorway.
// Lives in #decor (HTML over the scene, under the frogs) so it can light up for a win.
function finishHTML(th, celebrate) {
  if (typeof brewerySVG !== "function") return ""; // frog.js without the brewery: just the finish spot
  const p = G.pts[G.n], opts = { size: G.tiers ? 280 : 290, facing: p.face, celebrate, base: th.breweryBase };
  const b = brewerySVG.box(opts), left = Math.max(0, Math.min(DW - b.w, p.x - b.ax));
  return `<div class="finish" style="left:${left.toFixed(1)}px;top:${(p.y - b.ay).toFixed(1)}px">${brewerySVG(opts)}</div>`;
}

// Distant Front Range mountains (mountainsSVG in frog.js) along the top of a scene, when frog.js has them.
const mountains = (x, y, w, h, opts) => typeof mountainsSVG === "function" ? mountainsSVG({ x, y, w, h, at: 0.5, ...opts }) : "";

const BOTTLES = ["#7a4a1c", "#2f5a2a", "#8a7a5a", "#5a1e1e", "#c49a3a", "#3d5a6a"];
function lilyPad(x, y, s) {
  const rx = 58 * s, ry = 21 * s;
  return `<ellipse cx="${x + 4}" cy="${y + 6}" rx="${rx}" ry="${ry}" fill="#1d4b5c" opacity="0.5"/>
    <path d="M${x} ${y} L${x + rx * Math.cos(-0.45)} ${y + ry * Math.sin(-0.45)} A${rx} ${ry} 0 1 0 ${x + rx * Math.cos(0.05)} ${y + ry * Math.sin(0.05)} Z" fill="#3f8a3a" stroke="#24561f" stroke-width="3" stroke-linejoin="round"/>
    <path d="M${x} ${y} l${-rx * 0.6} ${-ry * 0.4} M${x} ${y} l${-rx * 0.7} ${ry * 0.35} M${x} ${y} l${rx * 0.2} ${ry * 0.8}" stroke="#5aab4c" stroke-width="2"/>`;
}

const THEMES = {
  // Gravity's bar: the bar top in front, the back-bar shelf above it.
  [THEME.BAR]: {
    ambient: true,
    background(G) {
      // Windows over the back bar look out west to the mountains at golden hour; bottles stand
      // on the shelf in the piers between them. Glass is tinted down so frogs read in front.
      const y = G.shelfY, xr = G.tiers ? G.tiers.xR - 40 : 1840, wt = G.tiers ? y - 146 : y - 270, wh = y - wt;
      const x0 = 30, pier = 150, nW = 3, ww = (xr + 30 - x0 - (nW + 1) * pier) / nW;
      const wins = Array.from({ length: nW }, (_, k) => x0 + pier + k * (ww + pier));
      const mid = wins[1] + ww / 2, sky = (typeof mountainsSVG === "function" && mountainsSVG.PALETTES.golden.sky) || ["#f0a868", "#ffe1a6"];
      let bottles = "";
      for (let i = 0, x = 60; x < xr; i++, x += 46 + ((i * 23) % 22)) {
        const h = 64 + ((i * 37) % 46), w = 22 + (i % 3) * 5, c = BOTTLES[i % BOTTLES.length];
        if (wins.some(l => x + w / 2 + 8 > l && x - w / 2 - 8 < l + ww)) continue;
        bottles += `<g opacity="0.7"><rect x="${x - w / 2}" y="${y - h}" width="${w}" height="${h}" rx="${w * 0.3}" fill="${c}"/>
          <rect x="${x - 4}" y="${y - h - 22}" width="8" height="26" rx="3" fill="${c}"/>
          <rect x="${x - w / 2 + 3}" y="${y - h * 0.62}" width="${w - 6}" height="${h * 0.28}" fill="#e8d6b8" opacity="0.45"/></g>`;
      }
      const glass = wins.map(x => `<rect x="${x}" y="${wt}" width="${ww}" height="${wh}" rx="6"/>`).join("");
      const frames = wins.map(x => `<rect x="${x}" y="${wt}" width="${ww}" height="${wh}" rx="6" fill="none" stroke="#5a3a1e" stroke-width="14"/>
          <path d="M${x} ${wt + wh * 0.24} H${x + ww} M${x + ww / 3} ${wt} V${wt + wh * 0.24} M${x + ww * 2 / 3} ${wt} V${wt + wh * 0.24}" stroke="#5a3a1e" stroke-width="8"/>
          <path d="M${x + ww * 0.12} ${wt + wh * 0.9} l${wh * 0.3} ${-wh * 0.7} M${x + ww * 0.6} ${wt + wh * 0.8} l${wh * 0.2} ${-wh * 0.45}" stroke="#fff" stroke-width="14" opacity="0.07"/>`).join("");
      return `<defs><radialGradient id="bWall" cx="0.5" cy="0.25" r="0.85"><stop offset="0" stop-color="#2b1a10"/><stop offset="1" stop-color="#140c07"/></radialGradient>
          <linearGradient id="bMirror" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#3a2a1e"/><stop offset="1" stop-color="#1c130c"/></linearGradient>
          <linearGradient id="bSky" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="${sky[0]}"/><stop offset="1" stop-color="${sky[1]}"/></linearGradient>
          <clipPath id="bWin">${glass}</clipPath>
          <radialGradient id="bGlow"><stop offset="0" stop-color="#ffd36b" stop-opacity="0.16"/><stop offset="1" stop-color="#ffd36b" stop-opacity="0"/></radialGradient></defs>
        <rect width="${DW}" height="${DH}" fill="url(#bWall)"/>
        <rect x="-20" y="${y - (G.tiers ? 150 : 280)}" width="${xr + 80}" height="${G.tiers ? 300 : 390}" rx="20" fill="url(#bMirror)" stroke="#5a3a1e" stroke-width="10"/>
        <g clip-path="url(#bWin)"><rect x="0" y="${wt}" width="${DW}" height="${wh}" fill="url(#bSky)"/>
          <circle cx="${wins[2] + ww * 0.7}" cy="${wt + wh * 0.3}" r="${wh * 0.13}" fill="#fff1c2" opacity="0.9"/>
          ${mountains(0, y - wh * 0.06, DW, wh * 0.68, { time: "golden", soft: 0.3, at: mid / DW, span: (ww * 0.78) / DW })}
          <rect x="0" y="${y - wh * 0.07}" width="${DW}" height="${wh * 0.07}" fill="#33432a"/>
          <rect x="0" y="${wt}" width="${DW}" height="${wh}" fill="#2a170b" opacity="0.3"/></g>
        ${frames}
        <ellipse cx="480" cy="${y - 60}" rx="360" ry="200" fill="url(#bGlow)"/><ellipse cx="1350" cy="${y - 60}" rx="360" ry="200" fill="url(#bGlow)"/>
        ${bottles}<rect x="-20" y="${y}" width="${xr + 80}" height="14" fill="#5a3720"/><rect x="-20" y="${y + 14}" width="${xr + 80}" height="6" fill="#2a170b"/>`;
    },
    bank(G) { // back-bar cabinets under the upper shelf, behind the bar top
      const t = G.tiers, top = t.yHi + 44, bot = t.yLo - 44, rx = t.rx - 50, ry = (bot - top) / 2;
      let doors = "";
      for (let x = 40; x + 200 < t.xR; x += 240) doors += `<rect x="${x}" y="${top + 26}" width="200" height="${bot - top - 52}" rx="10" fill="#4a2c17" stroke="#2a170b" stroke-width="6"/>
        <rect x="${x + 18}" y="${top + 44}" width="164" height="${bot - top - 88}" rx="6" fill="none" stroke="#5d3a1f" stroke-width="4"/>
        <circle cx="${x + 176}" cy="${(top + bot) / 2}" r="6" fill="#c49a3a"/>`;
      return `<path d="M-20 ${top} H${t.xR} A${rx} ${ry} 0 0 1 ${t.xR} ${bot} H-20 Z" fill="#3a2414"/>${doors}`;
    },
    path(G) { // one wooden ribbon: bar top, round the bend, shelf
      const y = G.pts[0].y, w = 92 * (G.fw / 160);
      let front = `<rect x="-20" y="${y + w * 0.5}" width="${DW + 40}" height="${DH}" fill="#5a3720"/>`;
      for (let x = 30; x < DW; x += 64) front += `<rect x="${x}" y="${y + w * 0.5 + 16}" width="4" height="${DH}" fill="#000" opacity="0.18"/>`;
      return `${front}
        <path d="${G.d}" stroke="#2a170b" stroke-width="${w + 12}" fill="none" stroke-linecap="round" stroke-linejoin="round" transform="translate(0 16)"/>
        <path d="${G.d}" stroke="#4a2a14" stroke-width="${w + 12}" fill="none" stroke-linecap="round" stroke-linejoin="round"/>
        <path d="${G.d}" stroke="#7a4a28" stroke-width="${w}" fill="none" stroke-linecap="round" stroke-linejoin="round"/>
        <path d="${G.d}" stroke="#000" stroke-width="${w}" fill="none" stroke-linecap="round" stroke-linejoin="round" filter="url(#rough)" opacity="0.3"/>
        <path d="${G.d}" stroke="#a06a3c" stroke-width="${w * 0.22}" fill="none" stroke-linecap="round" stroke-linejoin="round" opacity="0.45" transform="translate(0 ${-w * 0.28})"/>`;
    },
    spot(p, G, c = ["#d1ad75", "#e3c48f", "#f6e7c9"]) { // a beer coaster
      const k = G.fw / 160;
      return `<ellipse cx="${p.x}" cy="${p.y + 5 * k}" rx="${50 * k}" ry="${17 * k}" fill="#2a170b" opacity="0.6"/>
        <ellipse cx="${p.x}" cy="${p.y}" rx="${50 * k}" ry="${17 * k}" fill="${c[0]}"/>
        <ellipse cx="${p.x}" cy="${p.y}" rx="${42 * k}" ry="${14 * k}" fill="${c[1]}"/>
        <ellipse cx="${p.x}" cy="${p.y}" rx="${30 * k}" ry="${10 * k}" fill="${c[2]}"/>`;
    },
    start(p, G) { return this.spot(p, G, ["#6f9fca", "#9cc7ea", "#d9f0ff"]) + startTag(p, G); },
    finish(p, G) { return this.spot(p, G, ["#d99a1e", "#f5c542", "#fff3c4"]); },
  },
  // Lily pads on a lake at sunset: dock start, far shore, the brewery on a dock at the finish.
  [THEME.LAKE]: {
    background(G) {
      const hz = 196; // the far shore; the mountains rise above it
      let s = `<defs><linearGradient id="lSky" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#ffd9a8"/><stop offset="1" stop-color="#f6eccf"/></linearGradient>
        <linearGradient id="lWater" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#7fb7c4"/><stop offset="1" stop-color="#2f6f86"/></linearGradient></defs>
        <rect width="${DW}" height="${DH}" fill="url(#lSky)"/><circle cx="1640" cy="${hz - 140}" r="50" fill="#fff3c4"/>
        ${mountains(0, hz + 6, DW, hz - 16, { time: "golden", soft: 0.38 })}
        <path d="M0 ${hz - 2} Q300 ${hz - 14} 640 ${hz - 4} Q980 ${hz - 16} 1340 ${hz - 5} Q1650 ${hz - 12} 1920 ${hz - 3} V${hz + 10} H0 Z" fill="#46664a"/>
        <rect y="${hz}" width="${DW}" height="${DH - hz}" fill="url(#lWater)"/>`;
      for (let i = 0; i < 70; i++) {
        const x = (i * 211.7) % DW, y = hz + 30 + ((i * 97.1) % (DH - hz - 30)), w = 30 + (i % 5) * 14;
        s += `<path d="M${x - w} ${y} q${w} -6 ${w * 2} 0" stroke="#d8f1f4" stroke-width="3" fill="none" opacity="0.35" stroke-linecap="round"/>`;
      }
      const y0 = G.pts[0].y - 10;
      for (let i = 0; i < 14; i++) {
        const x = i < 7 ? 8 + i * 15 : DW - 18 - (i - 7) * 15, h = 120 + ((i * 37) % 90), b = (i % 3) - 1;
        s += `<path d="M${x} ${y0 + 60} q${i % 2 ? 8 : -8} ${-h / 2} ${b * 6} ${-h}" stroke="#3f5e2f" stroke-width="6" fill="none" stroke-linecap="round"/>
          <ellipse cx="${x + b * 6}" cy="${y0 + 60 - h + 16}" rx="7" ry="20" fill="#6b4423"/>`;
      }
      return s;
    },
    path(G) { // a faint current showing the route, plus a few loose pads
      let s = `<path d="${G.d}" stroke="#bfe6ee" stroke-width="120" fill="none" stroke-linecap="round" opacity="0.14"/>
        <path d="${G.d}" stroke="#e6f7fa" stroke-width="4" fill="none" stroke-dasharray="26 34" opacity="0.5"/>`;
      const t = G.tiers, y = G.pts[0].y;
      const deco = t ? [[t.xR - 150, t.cy - 70, 0.5], [t.xR - 60, t.cy + 80, 0.45], [130, 620, 0.55], [880, 630, 0.45], [1300, 250, 0.45]]
        : [[420, y - 330, 0.5], [1020, y - 420, 0.45], [1500, y - 300, 0.5], [760, y + 70, 0.4]];
      deco.forEach(([x, yy, k]) => (s += lilyPad(x, yy, k)));
      const fx = t ? t.xR - 110 : 1250, fy = t ? t.cy : y - 380;
      s += `<g transform="translate(${fx} ${fy})">${[0, 60, 120, 180, 240, 300].map(a => `<ellipse rx="7" ry="20" cy="-14" fill="#ffd6e8" stroke="#c77" stroke-width="2" transform="rotate(${a})"/>`).join("")}<circle r="8" fill="#ffd36b"/></g>`;
      return s;
    },
    spot: (p, G) => lilyPad(p.x, p.y, G.fw / 160),
    start(p, G) {
      const k = G.fw / 160, x0 = p.x - p.face * 30 * k;
      return `<g transform="translate(${x0} ${p.y}) scale(${k})">
        ${[-100, -60, -20, 20, 60].map(x => `<rect x="${x}" y="-18" width="38" height="46" rx="4" fill="#a8743f" stroke="#5a3a1c" stroke-width="3"/>`).join("")}
        <rect x="-104" y="26" width="12" height="40" fill="#5a3a1c"/><rect x="84" y="26" width="12" height="40" fill="#5a3a1c"/></g>` + startTag(p, G);
    },
    breweryBase: "dock",
  },
};
function pickTheme() {
  const q = new URLSearchParams(location.search).get("theme"); // ?theme=lake to preview one
  const id = THEMES[q] ? q : THEMES[S.theme] ? S.theme : THEME.BAR;
  return Object.assign({ id }, THEME_BASE, THEMES[id]);
}
function pickLayout() {
  const q = new URLSearchParams(location.search).get("layout");
  return LAYOUTS[q] ? q : LAYOUTS[S.layout] ? S.layout : LAYOUT.STRAIGHT;
}

// ---------- specials (launch pads and chutes) ----------
const trackLength = () => Math.max(1, (S.track && S.track.length) || S.trackLen || (S.stacks || []).length - 1 || 12);
function specials(n) {
  return ((S.track && S.track.specials) || []).filter(s => s && Object.values(SPOT).includes(s.kind) &&
    Number.isInteger(s.at) && Number.isInteger(s.to) && s.at > 0 && s.at < n && s.to >= 0 && s.to <= n && s.to !== s.at);
}
const bez = (c, t) => { const u = 1 - t; return { x: u * u * u * c[0].x + 3 * u * u * t * c[1].x + 3 * u * t * t * c[2].x + t * t * t * c[3].x,
  y: u * u * u * c[0].y + 3 * u * u * t * c[1].y + 3 * u * t * t * c[2].y + t * t * t * c[3].y }; };
// The slide from spot a down to spot b. Across tiers it's an S down the bank; on one row it bows
// out into the open space (up off the bottom row, down off a ledge).
function chuteCurve(a, b, G) {
  const dy = b.y - a.y, sx = Math.sign(b.x - a.x) || 1;
  if (Math.abs(dy) > 120) return [{ x: a.x, y: a.y + 4 }, { x: a.x, y: a.y + dy * 0.45 }, { x: b.x - 60 * sx, y: b.y - dy * 0.6 }, { x: b.x, y: b.y - 4 }];
  const bow = G.tiers && a.y < G.tiers.cy ? 150 : -120;
  return [{ x: a.x, y: a.y }, { x: a.x + (b.x - a.x) * 0.2, y: a.y + bow }, { x: b.x - (b.x - a.x) * 0.2, y: b.y + bow }, { x: b.x, y: b.y }];
}
// The launch arc between two ground points (also the frog's flight, shifted up to its perch).
function launchY(y0, y1, t) { const apex = Math.max(SKY, Math.min(y0, y1) - 300), h = (y0 + y1) / 2 - apex; return y0 + (y1 - y0) * t - 4 * t * (1 - t) * h; }
function padSVG(p, k, fill, ring, glyph) {
  const g = glyph === "up" ? `M${p.x - 12 * k} ${p.y + 6 * k} L${p.x} ${p.y - 11 * k} L${p.x + 12 * k} ${p.y + 6 * k} Z`
    : glyph === "down" ? `M${p.x - 12 * k} ${p.y - 9 * k} L${p.x} ${p.y + 8 * k} L${p.x + 12 * k} ${p.y - 9 * k} Z` : "";
  return `<ellipse cx="${p.x}" cy="${p.y}" rx="${58 * k}" ry="${22 * k}" fill="${ring}"/><ellipse cx="${p.x}" cy="${p.y - 2 * k}" rx="${48 * k}" ry="${16 * k}" fill="${fill}"/>
    ${g ? `<path d="${g}" fill="#fff" stroke="${ring}" stroke-width="${4 * k}" stroke-linejoin="round"/>` : ""}`;
}
function chuteSVG(a, b, G) {
  const c = chuteCurve(a, b, G), d = `M${c[0].x} ${c[0].y} C${c[1].x} ${c[1].y} ${c[2].x} ${c[2].y} ${c[3].x} ${c[3].y}`, k = G.fw / 160;
  return `<path d="${d}" stroke="#4d3a24" stroke-width="${64 * k}" fill="none" stroke-linecap="round" opacity="0.5" transform="translate(4 8)"/>
    <path d="${d}" stroke="#5b7fa0" stroke-width="${58 * k}" fill="none" stroke-linecap="round"/>
    <path d="${d}" stroke="#8fb4d4" stroke-width="${40 * k}" fill="none" stroke-linecap="round"/>
    <path d="${d}" stroke="#d4e7f5" stroke-width="${8 * k}" fill="none" stroke-linecap="round" stroke-dasharray="30 26" opacity="0.8"/>`;
}
function launchSVG(a, b, G) {
  const k = G.fw / 160;
  let d = "";
  for (let i = 0; i <= 48; i++) { const t = i / 48; d += `${i ? "L" : "M"}${(a.x + (b.x - a.x) * t).toFixed(1)} ${launchY(a.y - 30, b.y - 30, t).toFixed(1)} `; }
  return `<path d="${d}" stroke="#fff" stroke-width="${16 * k}" fill="none" stroke-linecap="round" stroke-dasharray="0.1 30" opacity="0.95"/>
    <path d="${d}" stroke="#2e9e4f" stroke-width="${9 * k}" fill="none" stroke-linecap="round" stroke-dasharray="0.1 30"/>
    <path d="M${b.x - 24 * k} ${b.y - 68 * k} L${b.x} ${b.y - 28 * k} L${b.x + 24 * k} ${b.y - 68 * k} Z" fill="#2e9e4f" stroke="#fff" stroke-width="4" stroke-linejoin="round"/>`;
}

// ---------- layout math ----------
let G = null, geoKey = "";
function layout() {
  const n = trackLength(), id = pickLayout();
  if (geoKey !== id + n) { geoKey = id + n; G = Object.assign({ id, n }, LAYOUTS[id].geometry(n)); }
  const fw = G.fw, P = i => G.pts[Math.max(0, Math.min(n, i))];
  // how far each frog in an h-high stack sits above the one under it: squeezed to stay under the ceiling
  const lift = (i, h) => h <= 1 ? fw * 0.5 : Math.min(fw * 0.5, Math.max(fw * 0.16, (P(i).y - fw - P(i).ceil) / (h - 1)));
  const gb = i => DH - P(i).y - fw * FOOT; // a frog's bottom edge standing on spot i
  const perch = (i, lvl, h) => ({ x: P(i).x, b: gb(i) + lvl * lift(i, h), g: gb(i) });
  const scr = (x, y) => { const r = $("track").getBoundingClientRect(), k = r.width / DW; return { x: r.left + x * k, y: r.top + y * k, k }; };
  return { fw, P, lift, gb, perch, scr };
}
function fitStage() {
  const k = Math.min(innerWidth / DW, innerHeight / DH);
  $("track").style.transform = `translate(${((innerWidth - DW * k) / 2).toFixed(1)}px, ${((innerHeight - DH * k) / 2).toFixed(1)}px) scale(${k})`;
}

let sceneKey = "";
function buildScene() {
  const th = pickTheme(), n = G.n, sp = specials(n), P = G.pts;
  const key = th.id + ":" + G.id + ":" + n + ":" + JSON.stringify(sp);
  if (sceneKey === key) return;
  sceneKey = key;
  document.body.classList.toggle("ambient", !!th.ambient);
  document.body.dataset.theme = th.id;
  const out = [`<defs>${SCENE_DEFS}</defs>`];
  out.push(th.image ? `<image href="${esc(th.image)}" width="${DW}" height="${DH}" preserveAspectRatio="xMidYMid slice"/>` : th.background(G));
  if (G.tiers && th.bank) out.push(th.bank(G));
  out.push(th.path(G));
  sp.filter(s => s.kind === SPOT.CHUTE).forEach(s => out.push(chuteSVG(P[s.at], P[s.to], G)));
  for (let i = 1; i < n; i++) out.push(th.spot(P[i], G));
  out.push(th.start(P[0], G), th.finish(P[n], G));
  const k = G.fw / 160;
  sp.forEach(s => out.push(s.kind === SPOT.LAUNCH
    ? launchSVG(P[s.at], P[s.to], G) + padSVG(P[s.at], k, "#4cd07a", "#1e6e37", "up") + padSVG(P[s.to], k, "#bff2cf", "#2e9e4f")
    : padSVG(P[s.at], k, "#e8574a", "#7a1d12", "down") + padSVG(P[s.to], k, "#f7c3bd", "#c0392b")));
  $("scene").innerHTML = out.join("");
  finishKey = "";
}
// The brewery lights up when a frog wins, and goes dark again for the next race.
let finishKey = "";
function renderFinish() {
  const th = pickTheme(), on = S.phase === HOP.phase.racing && S.sub === HOP.sub.raceEnd && !!S.winner;
  const key = sceneKey + on;
  if (finishKey === key) return;
  finishKey = key;
  $("decor").innerHTML = finishHTML(th, on);
}

function setFace(el, f) { el.classList.toggle("faceL", f === "L" || f === -1); }
