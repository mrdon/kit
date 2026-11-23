/* The frogs on the track: placing every stack, and the hop animation. */
function renderTrack() {
  const tr = $("track");
  fitStage();
  const L = layout(), n = G.n;
  buildScene();
  tr.style.setProperty("--fw", `${L.fw}px`);
  const hop = S.lastHop && S.lastHop.seq > lastHopSeq ? S.lastHop : null;
  if (hop) lastHopSeq = hop.seq;
  const leader = S.phase === "racing" ? S.ranking[0] : "";
  const now = performance.now();

  const seen = new Set();
  const flights = [], plan = hop && hopPlan(hop, n);
  S.stacks.forEach((stack, space) => {
    if (space > n) return;
    stack.forEach((id, idx) => {
      seen.add(id);
      const p = player(id); if (!p) return;
      let el = els[id], fresh = !el;
      if (!el) {
        el = document.createElement("div"); el.className = "frog";
        el.innerHTML = `<div class="sway"><div class="body"></div></div><div class="flies"><div class="fly" style="--fd:2.1s"></div><div class="fly" style="--fd:2.9s;--fdl:-1.2s"></div><div class="fly" style="--fd:2.5s;--fdl:-0.6s"></div></div>`;
        el.style.setProperty("--sd", (-Math.random() * 3) + "s");
        el.querySelectorAll('.fly').forEach(f => f.innerHTML = flySVG());
        tr.appendChild(el); els[id] = el;
      }
      // won a heat: a prize rosette pinned to the frog (drawn into its SVG, so it hops with it)
      const badge = !p.bot && p.wins > 0, k = p.color + p.hat + p.name + badge;
      if (el.dataset.key !== k) {
        el.dataset.key = k;
        el.querySelector(".body").innerHTML = frogSVG(p.color, p.hat, { badge });
      }
      const to = L.perch(space, idx, stack.length);
      // a hopping frog jumps from wherever it is on screen right now
      let from = null;
      if (hop && hop.moved.includes(id) && !fresh) {
        const cs = getComputedStyle(el);
        from = { x: parseFloat(cs.left), b: parseFloat(cs.bottom) };
        el.classList.add("flying");
      } else if (hop && hop.back && space === hop.to && !fresh) {
        // a backward hop slides UNDER this stack: it gets bumped up when the hopper arrives
        el.style.transitionDelay = `0s, ${Math.max(0, plan.arrive - 0.18).toFixed(2)}s`;
        setTimeout(() => { el.style.transitionDelay = ""; }, plan.arrive * 1000 + 600);
      }
      el.style.left = to.x + "px";
      el.style.bottom = to.b + "px";
      // nearer the bottom of the screen = nearer the camera = drawn in front
      el.dataset.z = Math.round(L.P(space).y / 4) * 2 + idx;
      el.style.zIndex = el.classList.contains("flying") ? 900 + idx : el.dataset.z;
      if (from) flights.push({ el, from, to, rider: hop.moved.indexOf(id) });
      el.style.setProperty("--amp", (idx === 0 ? 0.6 : 1.5 + idx * 1.4) + "deg");
      // face the way this stretch of track runs (the wrong way if turned around)
      const back = (S.backward || []).includes(id);
      el.classList.toggle("backward", back);
      el.dataset.face = L.P(space).face * (back ? -1 : 1) < 0 ? "L" : "R";
      if (!el.classList.contains("flying") && !(el._faceLock > now)) setFace(el, el.dataset.face);
      const cur = S.current === id && S.sub === "flick";
      el.classList.toggle("current", cur);
      el.classList.toggle("leader", id === leader);
      const body = el.querySelector(".body");
      const cheering = S.sub === "raceEnd" && id === S.winner;
      if (!body.classList.contains("hop")) body.classList.toggle("cheer", cheering);
      if (cur) { const sp = $("spot"); sp.style.left = to.x + "px"; sp.style.height = (L.P(space).y + 20) + "px"; }
    });
  });
  $("spot").classList.toggle("on", S.phase === "racing" && S.sub === "flick");
  for (const id in els) if (!seen.has(id)) { els[id].remove(); delete els[id]; }
  // Names under each spot, top of the stack first: find your frog from across the bar.
  const rfs = Math.max(18, Math.round(22 * Math.min(1, L.fw / 150)));
  $("rosters").innerHTML = S.stacks.map((st, space) => {
    if (!st.length || space > n) return "";
    const p0 = L.P(space), names = st.slice().reverse(), shown = names.length > 3 ? names.slice(0, 2) : names;
    return `<div class="roster" style="left:${p0.x}px;top:${p0.y + 16}px;width:${Math.round(G.step * 0.98)}px;--rfs:${rfs}px">${shown.map(id => {
      const p = player(id); if (!p) return "";
      return `<span class="${p.bot ? "" : "h"}" style="--c:${p.color}">${esc(p.name)}</span>`;
    }).join("")}${names.length > shown.length ? `<span>+${names.length - shown.length} more</span>` : ""}</div>`;
  }).join("");
  renderFinish();

  if (hop) { flyHop(hop, plan, flights, L); hopFX(hop, L, plan); }
}

// ---------- hopping ----------
// A move is one bounce per spot (so the crowd can count "1, 2, 3!"), each one:
// crouch on the ground -> stretch on takeoff -> parabola through the air -> splat landing.
// If the hop ends on a launch pad or a chute, a second leg follows: a big launch arc, or a slide.
const AIR0 = 0.22, AIR1 = 0.86; // fraction of a bounce spent crouching / in the air before landing
const LAUNCH_AT = [0.2, 0.88], CHUTE_END = 0.78; // launch leg: takeoff/touchdown; chute leg: end of the slide
function hopPlan(hop, nTrack) {
  const sp = hop.special && Object.values(SPOT).includes(hop.special.kind) ? hop.special : null;
  const clamp = i => Math.max(0, Math.min(nTrack, i | 0));
  const landed = clamp(sp ? (Number.isInteger(hop.landed) ? hop.landed : Number.isInteger(sp.at) ? sp.at : hop.to) : hop.to);
  const d = Math.abs(landed - hop.from), dir = landed >= hop.from ? 1 : -1;
  const n = Math.max(1, Math.min(6, d));
  const D = n === 1 ? 0.82 : Math.max(0.5, 0.72 - 0.07 * (n - 1)); // seconds per bounce
  const total = n * D, s0 = total + (sp ? 0.2 : 0), sDur = !sp ? 0 : sp.kind === SPOT.LAUNCH ? 1.5 : 1.9;
  const land = (n - 1 + AIR1) * D;
  // via(b): the spot the b-th touchdown lands on (0 = takeoff, n = landed)
  return { n, D, total, landed, special: sp, s0, sDur, end: s0 + sDur, launch: AIR0 * D, land,
    arrive: !sp ? land : s0 + sDur * (sp.kind === SPOT.LAUNCH ? LAUNCH_AT[1] : 0.97),
    touch: b => (b + AIR1) * D, via: b => hop.from + dir * Math.round(b * d / n) };
}
// frogs left behind on spot i (the hopper and its riders are already counted at the destination)
const stayCount = (hop, i) => (S.stacks[i] || []).filter(id => !hop.moved.includes(id)).length;
// squash & stretch per bounce: [phase, scaleX, scaleY, tilt (deg, nose-up is negative)]
const BOUNCE = [[0, 1, 1, 0], [0.14, 1.32, 0.68, 4], [0.24, 0.78, 1.28, -12], [0.36, 0.88, 1.16, -6], [0.5, 0.96, 1.06, 2],
  [0.64, 0.94, 1.08, 8], [0.8, 0.86, 1.18, 12], [0.86, 1.38, 0.64, 0], [0.93, 0.94, 1.08, -2], [1, 1, 1, 0]];
const airOf = u => Math.min(1, Math.max(0, (u - AIR0) / (AIR1 - AIR0)));
const bodyTf = (deg, sx, sy) => `translateY(-50%) rotate(${deg.toFixed(1)}deg) translateY(50%) scale(${sx.toFixed(3)}, ${sy.toFixed(3)})`;

function flyHop(hop, plan, flights, L) {
  if (!flights.length) return;
  const { n, D, total } = plan, sp = plan.special, T = sp ? plan.end : total, ms = T * 1000;
  const Hh = L.fw * 0.85 + G.step * 0.12; // apex height above the straight line between perches
  const STEPS = 24, tr = $("track"), movedN = hop.moved.length;
  flights.forEach(f => {
    // perches: where it took off, every touchdown on the way (on TOP of whatever stack stands there,
    // riders keeping their place), and the spot the hop reached
    const pts = [{ ...f.from, g: L.gb(hop.from) }];
    for (let b = 1; b < n; b++) { const c = plan.via(b), st = stayCount(hop, c); pts.push(L.perch(c, st + f.rider, st + movedN)); }
    const stL = stayCount(hop, plan.landed), land = sp ? L.perch(plan.landed, stL + f.rider, stL + movedN) : f.to;
    pts.push(land);
    const rel = (x, b) => `translate(calc(-50% + ${(x - f.to.x).toFixed(1)}px), ${(f.to.b - b).toFixed(1)}px)`;
    const path = [], shadow = [], faces = [];
    let face = f.el.classList.contains("faceL") ? -1 : 1;
    for (let b = 0; b < n; b++) {
      const p0 = pts[b], p1 = pts[b + 1];
      if (Math.abs(p1.x - p0.x) > 4) face = p1.x > p0.x ? 1 : -1; // face the way it's going (round the bend too)
      faces.push(face);
      const rise = Math.max(0, p1.b - p0.b) * 0.3; // climbing: arc a little higher
      for (let k = b ? 1 : 0; k <= STEPS; k++) {
        const u = k / STEPS, air = airOf(u);
        const h = 4 * air * (1 - air) * (Hh + rise) * (b === n - 1 ? 1.12 : 1);
        const x = p0.x + (p1.x - p0.x) * air, g = p0.g + (p1.g - p0.g) * air;
        // tall stacks up on the ledge flatten their arc rather than fly into the header
        const y = Math.min(p0.b + (p1.b - p0.b) * air + h, Math.max(DH - SKY - L.fw, p0.b, p1.b));
        const offset = ((b + u) * D) / T;
        path.push({ offset, transform: rel(x, y) });
        shadow.push({ offset, transform: `translate(calc(-50% + ${(x - f.to.x).toFixed(1)}px), ${(f.to.g - g).toFixed(1)}px) scale(${(1 - 0.55 * h / Hh).toFixed(3)})`,
          opacity: offset === 0 || (!sp && offset >= 1) ? 0 : air > 0 && air < 1 ? 1 - 0.5 * h / Hh : 0.7 });
      }
    }
    // second leg: launch or chute, carrying the riders, ending on top of whatever is at the far end
    const body = f.el.querySelector(".body"), frames = [];
    let legFace = face;
    const q2o = q => plan.s0 / T + q * (plan.sDur / T);
    if (sp) {
      path.push({ offset: q2o(0), transform: rel(land.x, land.b) });
      if (Math.abs(f.to.x - land.x) > 4) legFace = f.to.x > land.x ? 1 : -1;
      if (sp.kind === SPOT.LAUNCH) {
        const top = DH - L.fw - SKY + 10; // keep the whole frog under the header at the top of the arc
        path.push({ offset: q2o(LAUNCH_AT[0]), transform: rel(land.x, land.b) });
        for (let k = 1; k <= 40; k++) {
          const t = k / 40, x = land.x + (f.to.x - land.x) * t;
          const yb = Math.min(top, DH - launchY(DH - land.b, DH - f.to.b, t)); // same arc as the dotted line
          path.push({ offset: q2o(LAUNCH_AT[0] + (LAUNCH_AT[1] - LAUNCH_AT[0]) * t), transform: rel(x, yb) });
        }
        path.push({ offset: 1, transform: rel(f.to.x, f.to.b) });
        // crouch on the pad, stretch, a front flip on the way over, a big splat
        [[0, 0, 1, 1], [0.12, 0, 1.38, 0.62], [LAUNCH_AT[0], 0, 0.74, 1.32], [0.4, 140, 0.9, 1.12], [0.6, 260, 0.95, 1.05],
          [0.8, 350, 0.86, 1.18], [LAUNCH_AT[1], 360, 1.45, 0.58], [0.94, 360, 0.94, 1.08], [1, 360, 1, 1]]
          .forEach(([q, r, sx, sy]) => frames.push({ offset: q2o(q), transform: bodyTf(r * legFace, sx, sy) }));
      } else {
        const c = chuteCurve(L.P(plan.landed), L.P(hop.to), G), up = f.rider * L.fw * 0.42;
        for (let k = 0; k <= 40; k++) {
          const u = k / 40, t = Math.pow(u, 1.6), p = bez(c, t), p2 = bez(c, Math.min(1, t + 0.02)), p1 = bez(c, Math.max(0, t - 0.02));
          path.push({ offset: q2o(CHUTE_END * u), transform: rel(p.x, DH - p.y - L.fw * FOOT + up) });
          let a = Math.atan2(p2.y - p1.y, p2.x - p1.x) * 180 / Math.PI; // nose down the slope
          a = a > 90 ? a - 180 : a < -90 ? a + 180 : a;
          frames.push({ offset: q2o(CHUTE_END * u), transform: bodyTf(Math.max(-50, Math.min(50, a)), k ? 1.1 : 1, k ? 0.9 : 1) });
        }
        // hop off the end of the chute onto the top of the stack there
        const e = { x: c[3].x, b: DH - c[3].y - L.fw * FOOT + up };
        for (let k = 1; k <= 12; k++) {
          const t = k / 12;
          path.push({ offset: q2o(CHUTE_END + (1 - CHUTE_END) * t), transform: rel(e.x + (f.to.x - e.x) * t, e.b + (f.to.b - e.b) * t + 4 * t * (1 - t) * L.fw * 0.5) });
        }
        [[0.86, 0.8, 1.22], [0.97, 1.4, 0.6], [1, 1, 1]].forEach(([q, sx, sy]) => frames.push({ offset: q2o(q), transform: bodyTf(0, sx, sy) }));
      }
    }
    const a = f.el.animate(path, { duration: ms, easing: "linear" });
    a.onfinish = a.oncancel = () => { f.el.classList.remove("flying"); f.el.style.zIndex = f.el.dataset.z; };
    if (f.rider === 0) { // one ground shadow under the stack: sells the height
      const sh = document.createElement("div"); sh.className = "hopshadow";
      sh.style.left = f.to.x + "px"; sh.style.bottom = (f.to.g - L.fw * 0.05) + "px";
      tr.appendChild(sh);
      if (sp) shadow.push({ offset: total / T, opacity: 0, transform: shadow[shadow.length - 1].transform }, { offset: 1, opacity: 0, transform: shadow[shadow.length - 1].transform });
      sh.animate(shadow, { duration: ms, easing: "linear" }).onfinish = () => sh.remove();
    }
    // body: squash/stretch + tilt, and a backflip on the bounce where the frog turns around
    const hops = [];
    for (let b = 0; b < n; b++) for (const [u, sx, sy, tilt] of BOUNCE) {
      if (b && !u) continue;
      // only the frog that turned flips; riders just hang on (spinning them all scatters the stack)
      const spin = hop.turned && b === 0 && f.rider === 0 ? -360 * faces[0] * airOf(u) : 0;
      const last = !sp && b === n - 1 && u >= 0.86 ? 1.12 : 1; // the final landing splats hardest
      hops.push({ offset: ((b + u) * D) / T, transform: bodyTf(tilt * faces[b] + spin, sx > 1 ? sx * last : sx, sy < 1 ? sy / last : sy) });
    }
    // riders wobble a beat behind their carrier
    body.animate(hops.concat(frames), { duration: ms, delay: f.rider * 45, easing: "linear" });
    // the rig's leg-kick/arm-flail, retriggered on every bounce; facing follows each bounce
    body.style.setProperty("--bd", D + "s");
    f.el._faceLock = performance.now() + ms + 80;
    const kick = () => { body.classList.remove("hop", "cheer", "squish"); void body.offsetWidth; body.classList.add("hop"); };
    for (let b = 0; b < n; b++) setTimeout(() => { setFace(f.el, faces[b]); kick(); }, b * D * 1000 + f.rider * 45);
    if (sp) setTimeout(() => {
      setFace(f.el, legFace);
      if (sp.kind === SPOT.LAUNCH) { body.style.setProperty("--bd", plan.sDur * 0.7 + "s"); kick(); }
    }, plan.s0 * 1000 + f.rider * 45);
    setTimeout(() => { body.classList.remove("hop"); setFace(f.el, f.el.dataset.face); }, ms + f.rider * 45 + 50);
  });
}

// A ring of gold stars bursting out from (x, y), screen px.
function fxSparkle(x, y, size) {
  const ring = document.createElement("div");
  ring.className = "fx-ring"; ring.style.left = x + "px"; ring.style.top = y + "px"; ring.style.setProperty("--s", size * 2.6 + "px");
  document.body.appendChild(ring); setTimeout(() => ring.remove(), 900);
  for (let i = 0; i < 16; i++) {
    const d = document.createElement("div"), a = (i / 16) * Math.PI * 2, r = size * (0.9 + (i % 2) * 0.5);
    d.className = "fx-spark"; d.textContent = "✦";
    d.style.left = x + "px"; d.style.top = y + "px";
    d.style.setProperty("--s", size * (i % 2 ? 0.3 : 0.45) + "px");
    d.style.setProperty("--dx", Math.cos(a) * r + "px"); d.style.setProperty("--dy", Math.sin(a) * r + "px");
    document.body.appendChild(d); setTimeout(() => d.remove(), 950);
  }
}
function squishEl(el) { const u = el?.querySelector(".body"); if (u) { u.classList.remove("squish"); void u.offsetWidth; u.classList.add("squish"); } }
function hopFX(hop, L, plan) {
  const n = G.n, sp = plan.special, movedN = hop.moved.length, k = L.scr(0, 0).k, fw = L.fw * k;
  // screen point on top of an h-high stack on spot i
  const top = (i, h) => { const p = L.P(i); return L.scr(p.x, p.y - Math.max(0, h) * L.lift(i, Math.max(1, h))); };
  // the engine sets perfect only when the +1 bonus applied, and hop.roll already includes it
  const fromSize = (S.stacks[hop.from] || []).length, perfect = !!hop.perfect && !hop.back;
  const GOLD = "#f5c451", amt = hop.back ? `-${hop.roll}` : `+${Math.max(1, hop.roll)}`;
  setTimeout(() => { const p = top(hop.from, fromSize); fxPuff(p.x, p.y, fw * 0.35, 6); }, plan.launch * 1000);
  // a perfect flick: one sparkle burst on the frog and "Perfect!" over it
  if (perfect) {
    const p = top(hop.from, fromSize), el = els[hop.frog];
    fxSparkle(p.x, p.y - fw * 0.5, fw); fxPop(p.x, Math.max(innerHeight * 0.14, p.y - fw * 1.9), "Perfect!", fw * 0.7, GOLD);
    // the frog glows gold for the whole hop, so the room sees which hop got the bonus
    if (el) { el.classList.remove("golden"); void el.offsetWidth; el.classList.add("golden"); setTimeout(() => el.classList.remove("golden"), plan.land * 1000 + 600); }
  }
  // count the bounces out loud: 1… 2… then the big +3 on the last one
  for (let b = 0; b < plan.n - 1; b++) setTimeout(() => {
    const i = plan.via(b + 1), st = S.stacks[i] || [], p = top(i, st.length);
    if (st.length) squishEl(els[st[st.length - 1]]);
    fxPuff(p.x, p.y, fw * 0.25, 4);
    fxPop(p.x, p.y - fw * 1.2, `${b + 1}`, fw * 0.4, hop.back ? "#ffb3a7" : "#fff1b8");
  }, plan.touch(b) * 1000);
  setTimeout(() => {
    const i = plan.landed;
    if (hop.back && !sp) {
      const g = top(i, 0), p = top(i, (S.stacks[i] || []).length || 1);
      fxPuff(g.x, g.y, fw * 0.4, 8);
      fxPop(p.x, p.y - fw * 0.3, amt, fw * 0.55, "#ff9f9f");
      return;
    }
    const p = top(i, sp ? stayCount(hop, i) : (S.stacks[i] || []).length - movedN);
    fxPuff(p.x, p.y, fw * 0.4, 8);
    if (!sp && hop.landedOn) squishEl(els[hop.landedOn]);
    // one number: the whole hop, in the sparkle's gold when the flick was perfect
    fxPop(p.x, p.y - fw * 1.3, amt, fw * 0.55, hop.back ? "#ff9f9f" : perfect ? GOLD : "#b8ffb8");
    if (!sp && hop.to >= n) fxConfetti(160);
  }, plan.land * 1000);
  if (sp) {
    const launch = sp.kind === SPOT.LAUNCH;
    setTimeout(() => {
      const p = top(plan.landed, stayCount(hop, plan.landed) + movedN);
      fxPuff(p.x, p.y + fw * 0.2, fw * 0.4, 8);
      fxPop(p.x, p.y - fw * 0.6, launch ? "Launch" : "Chute", fw * 0.55, launch ? "#7dff9e" : "#ffb3a7");
    }, (plan.s0 + (launch ? plan.sDur * LAUNCH_AT[0] : 0)) * 1000);
    setTimeout(() => {
      const p = top(hop.to, (S.stacks[hop.to] || []).length - movedN);
      fxPuff(p.x, p.y, fw * 0.45, 9);
      if (hop.landedOn) squishEl(els[hop.landedOn]);
      if (hop.to >= n) fxConfetti(160);
    }, plan.arrive * 1000);
  }
}

// Whose turn: the name in Bungee gold fills the panel (long names shrink to fit)
// and bounces in when the turn changes, then holds still while people read it.
