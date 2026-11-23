// Scenery for the race: the brewery at the finish and the Front Range on the
// horizon. See frog.js for how these files are shared.

import { gravityLogoSVG } from './frog.js';

// Gravity Brewing as a little toy brewery: the finish line frogs race toward.
// Same ink + marker look as the frogs/hats (paper, marker fill, offset marker
// band, wobbly ink); the hop-drop logo and wordmark stay clean.
// A tall roll-up garage bay on the side the frogs come from is the finish
// doorway (chequered lintel + chequered threshold); the logo, wordmark and
// taproom window sit on the far half so the winning frog never covers them.
// opts: size      rendered height in px (default 280; width = size * 304/300)
//       facing    1 = frogs travel right (door on the left), -1 = frogs travel
//                 left (art mirrored, text and logo stay readable)
//       celebrate lights on, door rolled up on a glowing taproom, window lit
//                 with OPEN, steam from the brewhouse tank, rays round the logo
//       base      "slab" (default, concrete step) or "dock" (planks on pilings, for water)
// Anchor: the middle of the chequered threshold at ground level, see brewerySVG.box().
export function brewerySVG(opts = {}) {
  const W = 304, H = 300, G = 268, size = opts.size || 280, face = opts.facing < 0 ? -1 : 1, on = !!opts.celebrate;
  const I = "#1f1a17", P = "#f2ede1";
  let seed = 7; // fixed seed: same wobble every render, so toggling celebrate doesn't jitter
  const rnd = () => ((seed = (seed * 16807) % 2147483647) / 2147483647);
  const st = (o) => Object.entries(o).map(([k, v]) => `${k}:${v}`).join(";");
  // hand-drawn polyline: each straight edge bows a touch, corners jiggle
  const wob = (pts, closed = true, amp = 1.6) => {
    const q = pts.map(([x, y]) => [x + (rnd() - 0.5) * 0.9, y + (rnd() - 0.5) * 0.9]);
    if (closed) q.push(q[0]);
    let d = `M${q[0][0].toFixed(1)} ${q[0][1].toFixed(1)}`;
    for (let i = 1; i < q.length; i++) {
      const [x0, y0] = q[i - 1], [x1, y1] = q[i], len = Math.hypot(x1 - x0, y1 - y0) || 1;
      const b = (rnd() - 0.5) * Math.min(amp * 2, len * 0.05);
      d += ` Q${((x0 + x1) / 2 - ((y1 - y0) / len) * b).toFixed(1)} ${((y0 + y1) / 2 + ((x1 - x0) / len) * b).toFixed(1)} ${x1.toFixed(1)} ${y1.toFixed(1)}`;
    }
    return d + (closed ? " Z" : "");
  };
  const box = (x0, y0, x1, y1) => wob([[x0, y0], [x1, y0], [x1, y1], [x0, y1]]);
  const S = (d, c, o = {}) => `<path d="${d}" style="${st({ fill: P, stroke: "none" })}"/>` +
    `<path d="${d}" style="${st({ fill: c, "fill-opacity": o.op ?? 0.6, stroke: "none" })}"/>` +
    (o.band === false ? "" : `<path d="${d}" transform="translate(1.8 2)" style="${st({ fill: "none", stroke: o.band || c, "stroke-width": o.bw || 4.5, "stroke-linecap": "round", "stroke-linejoin": "round", opacity: 0.85 })}"/>`) +
    `<path d="${d}" style="${st({ fill: "none", stroke: I, "stroke-width": o.w || 2.6, "stroke-linecap": "round", "stroke-linejoin": "round" })}"/>`;
  const L = (d, w = 1.6, c = I, o = {}) => `<path d="${d}" style="${st({ fill: "none", stroke: c, "stroke-width": w, "stroke-linecap": "round", "stroke-linejoin": "round", ...o })}"/>`;
  const F = (d, c, op = 1) => `<path d="${d}" style="${st({ fill: c, "fill-opacity": op, stroke: "none" })}"/>`;
  const mx = (x) => (face > 0 ? x : W - x); // for the un-mirrored bits (logo, text)
  const font = `font-family:'Baloo 2','Arial Black',system-ui,sans-serif;font-weight:800`;
  const dx0 = 32, dx1 = 124, dy0 = 118, AX = (dx0 + dx1) / 2; // garage bay; AX = threshold middle

  // ---- mirrored structure (drawn for facing = 1) ----
  let s = "";
  // base: concrete step, or a dock on pilings for the lake
  if (opts.base === "dock") {
    for (const x of [18, 92, 166, 240]) s += S(box(x, G + 6, x + 12, H - 4), "#6b4423", { band: "#3e2510", bw: 3, w: 2 });
    s += L(`M4 ${H - 10} q14 -5 28 0 M86 ${H - 8} q14 -5 28 0 M160 ${H - 10} q14 -5 28 0 M234 ${H - 8} q14 -5 28 0`, 2, "#e6f7fa", { opacity: 0.7 });
    s += S(box(4, G - 1, 290, G + 11), "#a8743f", { band: "#6b4423", bw: 3.5 });
    s += L(`M66 ${G} v10 M136 ${G} v10 M206 ${G} v10 M258 ${G} v10`, 1.3, "#5a3a1c");
  } else {
    s += `<ellipse cx="150" cy="${G + 12}" rx="146" ry="9" style="fill:rgba(0,0,0,0.28)"/>`;
    s += S(box(10, G - 1, 292, G + 9), "#c9c3b5", { band: "#9b9485", bw: 3.5 });
  }
  // brewhouse tank behind the right wall, vent pipe on top
  s += S(`M252 ${G} L252 96 C252 78 298 78 298 96 L298 ${G} Z`, "#c4ced4", { band: "#8e9aa1", op: 0.7 }) +
    L(`M286 94 C287 140 287 200 286 ${G - 6}`, 4, "#ffffff", { opacity: 0.8 }) +
    L(`M268 140 C278 142 290 142 298 140 M268 206 C278 208 290 208 298 206`, 1.6) +
    `<circle cx="284" cy="172" r="5.5" style="fill:#e6ebee;stroke:${I};stroke-width:1.8"/>` +
    L(`M276 82 L276 56 Q276 48 284 48 L290 48`, 7) + L(`M276 82 L276 56 Q276 48 284 48 L290 48`, 3.6, "#c4ced4");
  if (on) { // steam from the vent: puffs drift up, swell and fade
    [0, 0.7, 1.4, 2.1].forEach((b, i) => {
      const a = `dur="2.8s" begin="${b}s" repeatCount="indefinite"`;
      s += `<circle cx="292" cy="46" r="6" style="fill:#ffffff;stroke:#b9c2c7;stroke-width:1.5;opacity:0">
        <animate attributeName="cy" values="46;-4" ${a}/><animate attributeName="cx" values="292;${270 - (i % 2) * 16}" ${a}/>
        <animate attributeName="r" values="6;19" ${a}/><animate attributeName="opacity" values="0;0.95;0" ${a}/></circle>`;
    });
  }
  // the building: white cinderblock with a stepped false front
  s += S(wob([[22, G], [22, 108], [46, 108], [46, 92], [238, 92], [238, 108], [262, 108], [262, G]]), "#e3e6e4", { band: "#b4bfc5", op: 0.5, bw: 5 });
  let joints = "";
  for (let y = 120, r = 0; y < G - 4; y += 12, r++) {
    joints += `M${dx1} ${y} H259 `;
    for (let x = 136 + (r % 2) * 12; x < 258; x += 24) joints += `M${x} ${y} v12 `;
  }
  s += L(joints, 1, "#b9c2c7", { opacity: 0.5 });
  s += L(wob([[22, 108], [46, 108], [46, 92], [238, 92], [238, 108], [262, 108]], false, 0.8), 7, "#3d434a");
  // garage bay (the finish doorway)
  if (on) {
    s += F(box(dx0, dy0, dx1, G), "#3a2414") + F(box(dx0 + 2, dy0 + 2, dx1 - 2, G), "#ffc85a", 0.75) +
      `<ellipse cx="${AX}" cy="${dy0 + 50}" rx="40" ry="36" style="fill:#fff3c4;opacity:0.55"/>`;
    s += F(box(dx0 + 2, G - 44, dx1 - 2, G), "#7a4a28", 0.9) + L(`M${dx0 + 2} ${G - 44} H${dx1 - 2}`, 2); // the bar, back wall
    for (const x of [40, 98]) { // copper fermenters: dome, body, cone, legs
      s += L(`M${x + 4} ${G - 2} v-16 M${x + 18} ${G - 2} v-16`, 2.2) +
        S(`M${x} ${G - 26} L${x} ${dy0 + 52} Q${x} ${dy0 + 40} ${x + 11} ${dy0 + 40} Q${x + 22} ${dy0 + 40} ${x + 22} ${dy0 + 52} L${x + 22} ${G - 26} L${x + 11} ${G - 16} Z`, "#e0a050", { band: "#a8652a", bw: 3, w: 1.8, op: 0.85 }) +
        L(`M${x + 6} ${dy0 + 50} V${G - 30}`, 2.2, "#fff3c4", { opacity: 0.85 });
    }
    s += L(`M${AX} ${dy0 + 12} v14`, 1.4) + `<circle cx="${AX}" cy="${dy0 + 32}" r="11" style="fill:#fff3c4;opacity:0.7"/>` +
      S(`M${AX - 8} ${dy0 + 32} Q${AX} ${dy0 + 22} ${AX + 8} ${dy0 + 32} Z`, "#2e7d32", { band: false, w: 1.5 });
    s += S(box(dx0 - 3, dy0, dx1 + 3, dy0 + 11), "#8e9aa1", { band: "#5f6a70", bw: 3, op: 0.75 }) +
      F(`M${dx0} ${G} L${dx1} ${G} L${dx1 + 22} ${G + 16} L${dx0 - 22} ${G + 16} Z`, "#ffd36b", 0.4);
  } else {
    s += S(box(dx0, dy0, dx1, G), "#aeb8be", { band: "#7d888e", op: 0.6 });
    let slats = "";
    for (let y = dy0 + 11; y < G - 2; y += 11) slats += `M${dx0 + 2} ${y} C${dx0 + 30} ${y + 0.8} ${dx1 - 30} ${y - 0.8} ${dx1 - 2} ${y} `;
    s += L(slats, 1.1, "#5f6a70") + L(`M${dx0 + 10} ${dy0 + 20} l12 -10 M${dx0 + 10} ${dy0 + 34} l26 -22`, 2.6, "#ffffff", { opacity: 0.6 }) +
      S(box(AX - 9, G - 13, AX + 9, G - 8), "#5f6a70", { band: false, w: 1.4 });
  }
  // chequered lintel over the bay
  let chk = "";
  for (let r = 0; r < 2; r++) for (let c = 0; c < 13; c++) if ((r + c) % 2 === 0) chk += `M${dx0 - 6 + c * 8} ${dy0 - 16 + r * 8} h8 v8 h-8 Z `;
  s += F(box(dx0 - 6, dy0 - 16, dx0 + 98, dy0), "#ffffff") + F(chk, I, 0.92) + L(box(dx0 - 6, dy0 - 16, dx0 + 98, dy0), 2.4);
  // chequered threshold mat in front of the bay (a little perspective)
  let mat = "";
  const mt = (u, v) => [dx0 + u * (dx1 - dx0) + (u - 0.5) * 16 * v, G + v * 12]; // u across, v towards the viewer
  for (let r = 0; r < 2; r++) for (let c = 0; c < 12; c++) if ((r + c) % 2 === 0) {
    const a = mt(c / 12, r / 2), b = mt((c + 1) / 12, r / 2), cc = mt((c + 1) / 12, (r + 1) / 2), d = mt(c / 12, (r + 1) / 2);
    mat += `M${a[0].toFixed(1)} ${a[1]} L${b[0].toFixed(1)} ${b[1]} L${cc[0].toFixed(1)} ${cc[1]} L${d[0].toFixed(1)} ${d[1]} Z `;
  }
  const edge = wob([mt(0, 0), mt(1, 0), mt(1, 1), mt(0, 1)], true, 0.4);
  s += F(edge, "#ffffff") + F(mat, I, 0.92) + L(edge, 2.2);
  // taproom window with a green awning, pallet planter under it
  const wx0 = 150, wx1 = 240, wy0 = 182, wy1 = 230;
  let awn = "";
  for (let i = 0; i < 6; i++) { const a = wx0 - 6 + i * 17, b = a + 17; awn += S(`M${a} 168 L${b} 168 L${b} 180 Q${a + 8.5} 187 ${a} 180 Z`, i % 2 ? P : "#2e7d32", { band: i % 2 ? "#cfc6ad" : "#1b5e20", bw: 2.6, w: 1.8, op: 0.75 }); }
  s += S(box(wx0, wy0, wx1, wy1), on ? "#ffd36b" : "#9fb6c2", { band: on ? "#f39a1e" : "#6f8c99", op: on ? 0.85 : 0.6 });
  if (on) s += `<rect x="${wx0 + 4}" y="${wy0 + 4}" width="${wx1 - wx0 - 8}" height="${wy1 - wy0 - 8}" style="fill:#fff3c4;opacity:0.4"/>`;
  else s += L(`M${wx0 + 8} ${wy1 - 8} l14 -28 M${wx0 + 24} ${wy1 - 8} l8 -16`, 2.4, "#ffffff", { opacity: 0.7 });
  s += L(`M${(wx0 + wx1) / 2} ${wy0 + 1} V${wy1 - 1} M${wx0 + 1} ${(wy0 + wy1) / 2} H${wx1 - 1}`, 2.2) + awn;
  for (const x of [162, 180, 198, 216, 232]) s += L(`M${x} 246 q-3 -10 -7 -12 M${x} 246 q0 -12 2 -15 M${x} 246 q4 -9 8 -10`, 3.2, "#3f8a3a");
  s += S(box(wx0, 244, wx1, 256), "#b88a55", { band: "#7a5028", bw: 3, w: 2 }) + S(box(wx0, 256, wx1, G - 1), "#b88a55", { band: "#7a5028", bw: 3, w: 2 });
  // string lights: swags along the parapet over the sign half (the bay has its chequered lintel)
  const swag = (x0, y0, x1, y1, sag) => {
    const cx = (x0 + x1) / 2, cy = (y0 + y1) / 2 + sag;
    let out = L(`M${x0} ${y0} Q${cx} ${cy} ${x1} ${y1}`, 1.4);
    for (let t = 0.12; t < 0.95; t += 0.19) {
      const x = (1 - t) * (1 - t) * x0 + 2 * t * (1 - t) * cx + t * t * x1, y = (1 - t) * (1 - t) * y0 + 2 * t * (1 - t) * cy + t * t * y1;
      out += (on ? `<circle cx="${x}" cy="${y + 5}" r="10" style="fill:#ffe08a;opacity:0.5"/>` : "") +
        `<ellipse cx="${x}" cy="${y + 5}" rx="3.4" ry="4.2" style="fill:${on ? "#ffd36b" : "#f6f0dc"};stroke:${I};stroke-width:1.2"/>` + L(`M${x} ${y} v1.6`, 2.2, "#3d434a");
    }
    return out;
  };
  s += swag(132, 108, 195, 98, 10) + swag(195, 98, 261, 110, 10);
  const flip = face > 0 ? "" : `translate(${W} 0) scale(-1 1)`;

  // ---- un-mirrored: logo medallion on the parapet, wordmark, window sign ----
  const cx = mx(195);
  let t = `<g transform="translate(${cx} 66)">${S(wob(Array.from({ length: 14 }, (_, i) => [31 * Math.cos(i * Math.PI / 7), 31 * Math.sin(i * Math.PI / 7)]), true, 0.6), "#ffffff", { band: "#f39a1e", op: 0.9, bw: 5 })}` +
    `<circle r="26" style="fill:#ffffff;stroke:#f39a1e;stroke-width:1.4;opacity:0.9"/>${gravityLogoSVG(24)}</g>`;
  if (on) t += L(`M${cx - 40} 40 l-9 -7 M${cx + 40} 40 l9 -7 M${cx - 45} 66 h-11 M${cx + 45} 66 h11`, 2.6, "#f39a1e");
  t += `<text x="${cx}" y="146" text-anchor="middle" textLength="114" lengthAdjust="spacingAndGlyphs" style="${font};font-size:31px;fill:${I}">GRAVITY</text>` +
    `<text x="${cx}" y="159" text-anchor="middle" textLength="74" lengthAdjust="spacingAndGlyphs" style="${font};font-size:10px;fill:${I}">◆ BREWING ◆</text>`;
  if (on) t += `<text x="${mx(195)}" y="211" text-anchor="middle" style="${font};font-size:17px;fill:#e74c3c;stroke:#fff3c4;stroke-width:3;paint-order:stroke">OPEN</text>`;

  const w = (size * W) / H;
  return `<svg class="brewery${on ? " celebrate" : ""}" viewBox="0 0 ${W} ${H}" width="${w.toFixed(1)}" height="${size}" xmlns="http://www.w3.org/2000/svg" style="overflow:visible">
    <g transform="${flip}">${s}</g>${t}</svg>`;
}
// Where to put it: { w, h } is the rendered size in px, (ax, ay) the anchor (middle of the
// chequered threshold, at ground level) measured from the svg's top-left corner. Place the svg at
// left = spotX - ax, top = spotGroundY - ay and the finish frog stands right in the doorway.
brewerySVG.box = (opts = {}) => {
  const h = opts.size || 280, k = h / 300;
  return { w: 304 * k, h, ax: (opts.facing < 0 ? 304 - 78 : 78) * k, ay: 268 * k };
};

// Distant Colorado Front Range as seen from Louisville: hazy snowy peaks (Longs Peak's
// massif to the north), rolling blue-grey foothills, and open plains with a ragged treeline
// in front. Soft scenery for the top of a race scene. Returns an SVG <g> fragment.
// opts: x, y     left end of the baseline (the art sits on y and rises above it)
//       w, h     size in px (h = top of the tallest peak above the baseline)
//       time     "day" (default) | "golden" | "dusk": palette (also mountainsSVG.PALETTES[time].sky)
//       at       where the range is centred, as a fraction of w (default 0.42)
//       soft     0..1 haze into the sky (default 0.35)
//       seed     wobble seed (default 11)
export function mountainsSVG(opts = {}) {
  const X = opts.x || 0, Y = opts.y || 0, w = opts.w || 1920, h = opts.h || 160;
  const pal = mountainsSVG.PALETTES[opts.time] || mountainsSVG.PALETTES.day;
  const span = opts.span || Math.min(0.42, 560 / w), at = opts.at ?? 0.42, nSlab = Math.max(1, Math.min(5, opts.slabs || 5));
  const soft = Math.max(0, Math.min(1, opts.soft ?? 0.35)), lw = Math.max(0.8, Math.min(1.8, h / 120));
  let seed = opts.seed || 11;
  const rnd = () => ((seed = (seed * 16807) % 2147483647) / 2147483647);
  const hex = c => [1, 3, 5].map(i => parseInt(c.slice(i, i + 2), 16));
  const mix = (a, b, t) => "#" + hex(a).map((v, i) => Math.round(v + (hex(b)[i] - v) * t).toString(16).padStart(2, "0")).join("");
  // aerial perspective: each layer fades toward the haze by its depth, and soft pushes them all back
  const hz = (c, d) => mix(c, pal.haze, Math.min(0.92, d + soft * (1 - d) * 0.55));
  const f2 = n => n.toFixed(1);
  // points are local [x, y]: x right along the baseline, y up from it
  const path = (pts, closed = true) => {
    const q = pts.map(([x, y]) => [X + x, Y - y]);
    let d = `M${f2(q[0][0])} ${f2(q[0][1])}`;
    for (let i = 1; i < q.length; i++) {
      const [x0, y0] = q[i - 1], [x1, y1] = q[i], len = Math.hypot(x1 - x0, y1 - y0) || 1, b = (rnd() - 0.5) * Math.min(2.4, len * 0.05);
      d += ` Q${f2((x0 + x1) / 2 - ((y1 - y0) / len) * b)} ${f2((y0 + y1) / 2 + ((x1 - x0) / len) * b)} ${f2(x1)} ${f2(y1)}`;
    }
    return d + (closed ? " Z" : "");
  };
  const line = (pts, color, width, op) => `<path d="${path(pts, false)}" fill="none" stroke="${color}" stroke-width="${f2(width)}" stroke-linecap="round" stroke-linejoin="round" opacity="${op}"/>`;
  // paper, marker fill, a faint offset marker band along the top edge, soft wobbly ink
  const shape = (pts, fill, band, top, ink, io) => `<path d="${path(pts)}" fill="${pal.paper}"/><path d="${path(pts)}" fill="${fill}" fill-opacity="0.9"/>` +
    `<path d="${path(top, false)}" fill="none" stroke="${band}" stroke-width="${f2(lw * 2.2)}" stroke-linecap="round" stroke-linejoin="round" opacity="0.45" transform="translate(${f2(lw * 0.8)} ${f2(lw)})"/>` +
    line(top, ink, lw, io);
  const ridge = (f, step) => { // f(x) = height in px at local x
    const pts = [];
    for (let x = 0; x <= w + step / 2; x += step) pts.push([Math.min(x, w), f(Math.min(x, w))]);
    return pts;
  };
  const close = top => [...top, [w, 0], [0, 0]];
  const lerp = (a, b, t) => [a[0] + (b[0] - a[0]) * t, a[1] + (b[1] - a[1]) * t];
  const tent = (t, c, top, slope) => top - slope * Math.abs(t - c);
  const gw = Math.min(span * w, h * 1.5), gx0 = at * w - gw / 2, U = u => gx0 + u * gw; // slab-group units: 0..1 across the group
  const tL = Math.min(0.94, at + 0.32); // Longs Peak, north-west of town
  let s = "";

  // 1. far Front Range: hazy blue-grey, snow on the high ones (the Indian Peaks, then Longs and Meeker)
  const farF = x => {
    const t = x / w;
    const k = w / h; // slopes scale with the aspect, so peaks keep their shape at any width
    let v = 0.5 + 0.05 * Math.sin(t * 13 + 1) + 0.03 * Math.sin(t * 37 + 2);
    v = Math.max(v, tent(t, tL, 0.93, 0.5 * k), tent(t, tL + 0.03, 0.87, 0.6 * k), tent(t, tL - 0.05, 0.76, 0.55 * k),
      tent(t, at + 0.12, 0.7, 0.7 * k), tent(t, at + 0.17, 0.77, 0.65 * k), tent(t, at + 0.22, 0.72, 0.7 * k),
      tent(t, at - 0.3, 0.64, 0.45 * k), tent(t, at - 0.4, 0.69, 0.5 * k), tent(t, Math.max(0.04, at - 0.55), 0.62, 0.45 * k));
    if (Math.abs(t - tL) < 0.06 / k) v = Math.max(v, 0.925); // Longs' flat top
    return (v + (rnd() - 0.5) * 0.018) * h;
  };
  const far = ridge(farF, Math.max(6, w / 160));
  const farInk = hz(pal.ink, 0.6);
  s += shape(close(far), hz(pal.far, 0.42), hz(pal.farShade, 0.42), far, farInk, 0.35);
  // snowfields hang down from the high ridges in ragged tongues
  const snowLine = 0.64 * h;
  for (let i = 0; i < far.length;) {
    if (far[i][1] < snowLine) { i++; continue; }
    const run = [];
    while (i < far.length && far[i][1] >= snowLine) run.push(far[i++]);
    if (run.length < 2) continue;
    const bot = run.map(([x, y], j) => [x, j === 0 || j === run.length - 1 ? y - h * 0.01 : Math.max(snowLine - h * (0.02 + rnd() * 0.1), y - h * (0.03 + rnd() * 0.13))]).reverse();
    s += `<path d="${path([...run, ...bot])}" fill="${hz(pal.snow, 0.25)}" opacity="0.9"/>`;
  }
  // 2. rolling foothills between, blue-grey with a little green
  const foot = ridge(x => (0.44 + 0.07 * Math.sin(x / w * 9 + 0.5) + 0.04 * Math.sin(x / w * 23) + (rnd() - 0.5) * 0.012) * h, Math.max(5, w / 220));
  s += shape(close(foot), hz(pal.foot, 0.3), hz(pal.farShade, 0.3), foot, hz(pal.ink, 0.45), 0.35);
  // nearer wooded ridges either side of Green Mountain, half way between the blue and the green
  const near = ridge(x => (0.3 + 0.06 * Math.sin(x / w * 15 + 2) + 0.04 * Math.sin(x / w * 41 + 1) + (rnd() - 0.5) * 0.01) * h, Math.max(4, w / 300));
  s += shape(close(near), hz(mix(pal.mtn, pal.foot, 0.5), 0.22), hz(pal.pine, 0.25), near, hz(pal.ink, 0.35), 0.35);

  // 5. the open mesa and plains in front, with a ragged line of trees along it
  const mesa = ridge(x => (0.1 + 0.035 * Math.sin(x / w * 7 + 1) + 0.03 * Math.max(0, 1 - Math.abs(x - U(0.4)) / gw)) * h, Math.max(5, w / 200));
  s += shape(close(mesa), hz(pal.mesa, 0.15), hz(pal.mesaBand, 0.15), mesa, hz(pal.ink, 0.3), 0.35);
  const tl = [[0, h * 0.03]];
  for (let x = 0; x < w;) {
    const tw = Math.max(3, h * (0.02 + rnd() * 0.02)), up = rnd() < 0.55;
    tl.push([x + tw / 2, h * (up ? 0.05 + rnd() * 0.05 : 0.035)], [x + tw, h * 0.03]);
    x += tw;
  }
  tl.push([w, h * 0.03]);
  s += `<path d="${path(close(tl))}" fill="${hz(pal.pine, 0.08)}"/>` + line(tl, hz(pal.ink, 0.2), lw * 0.8, 0.3);
  return `<g class="mountains">${s}</g>`;
}
mountainsSVG.PALETTES = {
  day: { sky: ["#9fc4e4", "#e6eef2"], paper: "#f2ede1", ink: "#3a3a44", haze: "#dce6ee", far: "#8d9db3", farShade: "#71829b", snow: "#f1f4f7",
    foot: "#6f8092", mtn: "#2f4334", mtnLit: "#566a4c", pine: "#24342a", mesa: "#8f9068", mesaBand: "#74774f",
    slab: "#b29a88", slabBand: "#8c7264", slabShade: "#76625a", slabLit: "#dccab8" },
  golden: { sky: ["#f0a868", "#ffe1a6"], paper: "#f5e9d4", ink: "#4a3a36", haze: "#f3d6b0", far: "#9a92aa", farShade: "#7f7792", snow: "#f8e6d8",
    foot: "#78798a", mtn: "#36432f", mtnLit: "#62684a", pine: "#2a3527", mesa: "#9a8d62", mesaBand: "#7c7049",
    slab: "#bf9a84", slabBand: "#94705f", slabShade: "#7c6058", slabLit: "#e8caa9" },
  dusk: { sky: ["#4f5a8a", "#d99a8a"], paper: "#e9dfd6", ink: "#2c2838", haze: "#c7a6a8", far: "#6f6f92", farShade: "#5b5b7e", snow: "#d8d4e6",
    foot: "#5b5e76", mtn: "#2e3a35", mtnLit: "#475446", pine: "#222b27", mesa: "#6a6458", mesaBand: "#55504a",
    slab: "#987b73", slabBand: "#745a55", slabShade: "#5c4949", slabLit: "#c3a69b" },
};
