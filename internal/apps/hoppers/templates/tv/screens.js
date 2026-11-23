/* The turn panel, the results and standings sequence, and the per-frame
   countdowns. */
let turnHTML = "", turnWho = "";
function renderTurn() {
  const t = $("turn");
  let who = null, html;
  const cur = player(S.current), deck = player(S.onDeck), h = S.lastHop;
  const nextTxt = p => deck && deck !== p ? ` <span>· next ${esc(deck.name)}</span>` : "";
  if (S.phase !== HOP.phase.racing) {
    html = `<div class="big">${S.phase === HOP.phase.final ? "That's the night" : "Waiting for frogs…"}</div>`;
  } else if (S.sub === HOP.sub.ready) {
    who = player((S.turnOrder || [])[0]);
    html = who ? `<div class="what">Goes first</div>` : `<div class="big">Get ready</div>`;
  } else if (S.sub === HOP.sub.flick && cur) {
    who = cur;
    html = `<div class="what">${cur.bot ? "🤖 flicking" : "Flick now"}${nextTxt(cur)}</div><div class="bar"><div id="tbar"></div></div>`;
  } else if (S.sub === HOP.sub.hop && h) {
    who = player(h.frog);
    const perfect = h.perfect && !h.back;
    const amt = (h.roll === 0 ? "stays put" : h.back ? `back ${h.roll}` : `+${h.roll}${perfect ? " · Perfect!" : ""}`) +
      (h.special?.kind === SPOT.LAUNCH ? ", launched" : h.special?.kind === SPOT.CHUTE ? ", down the chute" : "");
    html = `<div class="what ${perfect ? "perfect" : ""}">${amt}${nextTxt(who)}</div>`;
  } else if (S.sub === HOP.sub.raceEnd) {
    const nx = nextLine();
    html = `<div class="big">${esc(nx.head || "Race over")}</div>${nx.next ? `<div class="next">${esc(nx.next)}</div>` : ""}`;
  } else {
    html = `<div class="big">${esc(raceLabel())}</div>`;
  }
  const name = who ? who.name : "";
  if (html === turnHTML && name === turnWho) return;
  const changed = name !== turnWho;
  turnHTML = html; turnWho = name;
  t.innerHTML = (who ? `<div class="who ${changed ? "k-in-bounce" : ""}">${esc(name)}</div>` : "") + html;
  fitText(t.querySelector(".who"), 6.4, 3);
}

function renderResults() { GP.render(); }
// the heat lining up after this one's results ("next"), if any
const nextHeat = () => (S.heats || []).find(x => x.status === HOP.heat.next);

// ---------- results sequence ----------
// After every race (Mario Kart Grand Prix style): that race's finishing order,
// then the same panel becomes the night's standings, this race's points count
// into each total, and rows slide to their new ranks with a ▲/▼ for movement.
// Fixed pacing (~6s), then it holds on the standings until the host moves on
// (results have no deadline). After a round's last race this IS the beer break
// (sub raceEnd + roundOver): the foot line says so and what's next.
// Tuned in mid-results: straight to the end state.
const GP = (() => {
  let key = "", timers = [], raf = 0, sawRace = false;
  const box = () => document.getElementById("gp");
  const ord = n => { const s = ["th", "st", "nd", "rd"], v = n % 100; return n + (s[(v - 20) % 10] || s[v] || s[0]); };
  const clear = () => { timers.forEach(clearTimeout); timers = []; cancelAnimationFrame(raf); };
  const at = (ms, fn) => timers.push(setTimeout(fn, ms));

  // Night table before and after this race. Before = points minus this race's
  // points, sorted with the engine's tie-break (scoring.go standingIDs: points,
  // then heat wins, then join order; the snapshot lists players in join order).
  function table(s) {
    const res = new Map((s.results || []).map(r => [r.player || r.frog, r]));
    const join = new Map((s.players || []).map((p, i) => [p.id, i]));
    const rows = (s.standings || []).filter(x => player(x.player)).map((x, i) => {
      const r = res.get(x.player), gain = r && r.points > 0 ? r.points : 0, pts = x.points | 0;
      return { id: x.player, rank: x.rank || i + 1, pts, gain, prevPts: Math.max(0, pts - gain),
        prevWins: Math.max(0, (x.wins | 0) - (r && r.place === 1 && gain > 0 ? 1 : 0)),
        join: join.has(x.player) ? join.get(x.player) : 1e6 + i };
    });
    rows.slice().sort((a, b) => b.prevPts - a.prevPts || b.prevWins - a.prevWins || a.join - b.join)
      .forEach((r, i) => { r.prevRank = i + 1; });
    return rows.sort((a, b) => a.rank - b.rank);
  }

  // Up to 12 rows in one column; more go in two (30 people = 2 x 15 at ~5.5vh).
  function geom(n) {
    const cols = n > 12 ? 2 : 1, per = Math.max(1, Math.ceil(n / cols));
    return { cols, per, rh: Math.min(cols > 1 ? 6.4 : 7.4, 82 / per), colW: cols > 1 ? 44 : 56, gap: 1.6 };
  }
  const slot = (g, i) => `translate(${Math.floor(i / g.per) * (g.colW + g.gap)}vw, ${(i % g.per) * g.rh}vh)`;
  function sizeBody(body, g, n) {
    body.style.setProperty("--rh", g.rh + "vh");
    body.style.width = (g.cols * g.colW + (g.cols - 1) * g.gap) + "vw";
    body.style.height = Math.min(n, g.per) * g.rh + "vh";
  }
  const frog = (p, opts = {}) => `<span class="fr">${frogSVG(p.color, p.hat, { badge: !p.bot && p.wins > 0, ...opts })}</span>`;
  function head(gp, title, sub) {
    const h = gp.querySelector("h3");
    if (h.textContent !== title) { h.textContent = title; h.classList.remove("swap"); void h.offsetWidth; h.classList.add("swap"); }
    gp.querySelector(".gp-sub").textContent = sub;
  }

  function showResults(gp, s, k) {
    const res = (s.results || []).filter(r => player(r.player || r.frog)).sort((a, b) => (a.place | 0) - (b.place | 0));
    const g = geom(res.length), body = gp.querySelector(".gp-body"), w = player(s.winner);
    const race = s.final ? "The Final" : multiHeat() ? s.heatName || `Heat ${s.heat}` : `Race ${s.round}`;
    head(gp, `${race} results`, w ? `${w.name} wins` : "");
    sizeBody(body, g, res.length);
    const stag = Math.min(110, 1100 / Math.max(1, res.length)) * k;
    body.innerHTML = res.map((r, i) => {
      const p = player(r.player || r.frog), win = r.place === 1;
      return `<div class="gp-row" style="width:${g.colW}vw;transform:${slot(g, i)}"><div class="gp-card in ${win ? "win" : ""} ${p.bot ? "bot" : ""}" style="animation-delay:${Math.round(i * stag)}ms">
        <span class="rk">${ord(r.place | 0)}</span>${win ? `<span class="fr k-wobble">${frogSVG(p.color, p.hat, { badge: !p.bot && p.wins > 0, mood: "wow" })}</span>` : frog(p)}<span class="nm">${esc(p.name)}</span><span class="pt">${p.bot ? "bot" : "+" + (r.points | 0)}</span></div></div>`;
    }).join("");
  }

  // Standings rows at their old ranks with old totals (done = straight to the end state).
  function showStandings(gp, s, rows, done) {
    const g = geom(rows.length), body = gp.querySelector(".gp-body");
    head(gp, "Standings", s.final ? "After The Final" : multiHeat() ? `Round ${s.round} · after ${s.heatName || "Heat " + s.heat}` : `After race ${s.round} of ${s.rounds}`);
    sizeBody(body, g, rows.length);
    body.classList.remove("moving");
    body.innerHTML = rows.map((r, i) => {
      const p = player(r.id), idx = (done ? r.rank : r.prevRank) - 1, lead = done && r.rank === 1 && r.pts > 0;
      return `<div class="gp-row" data-i="${i}" style="width:${g.colW}vw;transform:${slot(g, idx)}"><div class="gp-card fade ${r.gain ? "ran" : ""} ${lead ? "lead" : ""}">
        <span class="rk">${done ? r.rank : r.prevRank}</span><span class="mv"></span>${frog(p)}<span class="nm">${esc(p.name)}</span>
        <span class="gain ${done ? "gone" : ""}">${r.gain ? "+" + r.gain : ""}</span><span class="tot">${done ? r.pts : r.prevPts}</span></div></div>`;
    }).join("");
    return g;
  }

  // "+N" flies into the total, which ticks up.
  function countUp(gp, rows, dur) {
    const els = rows.map((r, i) => r.gain ? gp.querySelector(`.gp-row[data-i="${i}"]`) : null);
    els.forEach(e => e && e.querySelector(".gain").classList.add("gone"));
    const t0 = performance.now();
    const step = now => {
      const f = Math.min(1, (now - t0) / dur), ease = 1 - (1 - f) * (1 - f);
      els.forEach((e, i) => { if (e) e.querySelector(".tot").textContent = Math.round(rows[i].prevPts + rows[i].gain * ease); });
      if (f < 1) raf = requestAnimationFrame(step);
      else els.forEach(e => { if (e) { const t = e.querySelector(".tot"); t.classList.remove("bump"); void t.offsetWidth; t.classList.add("bump"); } });
    };
    raf = requestAnimationFrame(step);
  }

  // Rows slide to their new ranks. Arrows only once there was a table to move in.
  function reorder(gp, rows, g, dur) {
    const body = gp.querySelector(".gp-body"), arrows = rows.some(r => r.prevPts > 0);
    body.classList.add("moving");
    rows.forEach((r, i) => {
      const e = gp.querySelector(`.gp-row[data-i="${i}"]`); if (!e) return;
      const d = r.prevRank - r.rank, mv = e.querySelector(".mv");
      e.style.transitionDuration = dur + "ms";
      e.classList.toggle("up", d > 0);
      e.style.transform = slot(g, r.rank - 1);
      e.querySelector(".rk").textContent = r.rank;
      e.querySelector(".gp-card").classList.toggle("lead", r.rank === 1 && r.pts > 0);
      if (arrows && d) { mv.textContent = (d > 0 ? "▲" : "▼") + Math.abs(d); mv.className = `mv ${d > 0 ? "up" : "dn"} pop`; }
    });
  }

  function start(s) {
    clear();
    const gp = box(); if (!gp) return;
    const k = 1, late = !sawRace;
    const rows = table(s), hasRes = (s.results || []).length > 0;
    if (!rows.length && !hasRes) { gp.classList.remove("show"); return; }
    const nx = nextLine();
    gp.innerHTML = `<div class="gp-head"><h3></h3><div class="gp-sub"></div></div><div class="gp-body"></div>
      <div class="gp-foot">${nx.head ? `<b>${esc(nx.head)}</b>${nx.next ? " · " : ""}` : ""}${esc(nx.next)}</div>`;
    gp.classList.remove("show"); void gp.offsetWidth; gp.classList.add("show");
    // Tuned in late, or nothing to count: just show where things stand.
    if (!hasRes || late) { if (rows.length) showStandings(gp, s, rows, true); else if (hasRes) showResults(gp, s, k); return; }
    showResults(gp, s, k);
    if (!rows.length) return;
    let g;
    at(3000 * k, () => { g = showStandings(gp, s, rows, false); });
    at(3550 * k, () => countUp(gp, rows, 700 * k));
    at(4500 * k, () => reorder(gp, rows, g, 900 * k));
  }

  function render() {
    const gp = box(); if (!gp) return;
    if (!S || S.phase !== HOP.phase.racing || S.sub !== HOP.sub.raceEnd) {
      if (S && S.phase === HOP.phase.racing) sawRace = true;
      if (key) { clear(); key = ""; gp.classList.remove("show"); gp.innerHTML = ""; }
      return;
    }
    const k = `${S.round}-${S.heat}-${(S.results || []).map(r => r.player).join(",")}`;
    if (k === key) return;
    key = k;
    const fresh = sawRace;
    start(S);
    sawRace = false;
    if (fresh) fxConfetti(220);
  }
  return { render: () => { render(); const gp = box(); document.body.classList.toggle("gp-on", !!gp && gp.classList.contains("show")); } };
})();

function tickBar() {
  const n = document.getElementById("readyN");
  if (n && S && S.sub === "ready") {
    const c = Math.max(0, Math.ceil((S.deadline - (Date.now() + clockSkew)) / 1000));
    if (c !== lastCount) { lastCount = c; n.textContent = c || "GO"; n.classList.remove("tick"); void n.offsetWidth; n.classList.add("tick"); }
  }
  const b = document.getElementById("tbar");
  if (b && S && S.sub === "flick" && S.deadline) {
    const left = S.deadline - (Date.now() + clockSkew);
    b.style.width = Math.max(0, Math.min(100, left / 80)) + "%";
    b.classList.toggle("warn", left < 3000 && left >= 1500); b.classList.toggle("hot", left < 1500);
  }
  requestAnimationFrame(tickBar);
}
