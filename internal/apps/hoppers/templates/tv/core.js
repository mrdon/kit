/* The Hoppers TV: state, the render dispatch, and the screens around the race
   (lobby pond, ready countdown, champion). Concatenated with the other tv/
   files into one IIFE by render.go, after the shared art (frog, fx, scenery)
   and HOP constants, so every file sees every other's declarations. */
let S = null, clockSkew = 0, lastEventSeq = 0, lastHopSeq = 0, lastPhase = "";
const els = {};            // frog id -> element
const pondSeen = new Set(); // frogs already shown in the pond
const $ = id => document.getElementById(id);

// ---------- ambience ----------
function wiggleText(el, text) {
  el.innerHTML = [...text].map((c, i) => `<span style="animation-delay:${(i * 0.12).toFixed(2)}s">${c === " " ? "&nbsp;" : c}</span>`).join("");
}
wiggleText($("title"), "Hoppers");
wiggleText($("lobbyTitle"), "Hoppers");
(function lights() {
  const n = 28, cols = ["#ffd36b", "#ff9f6b", "#fff1b8", "#ffb3c7", "#b8f0ff"];
  let path = "M0 10 ", bulbs = "";
  for (let i = 0; i <= n; i++) {
    const x = (i / n) * 1000, y = 10 + (i % 2 ? 26 : 10);
    path += `L${x} ${y} `;
    bulbs += `<g class="bulb" style="animation-delay:${(Math.random() * 3).toFixed(2)}s">
      <circle cx="${x}" cy="${y + 10}" r="9" fill="${cols[i % cols.length]}" opacity="0.25"/>
      <ellipse cx="${x}" cy="${y + 9}" rx="4" ry="6" fill="${cols[i % cols.length]}"/></g>`;
  }
  $("lights").innerHTML = `<svg viewBox="0 0 1000 60" preserveAspectRatio="none"><path d="${path}" stroke="#3a2a1a" stroke-width="2" fill="none"/>${bulbs}</svg>`;
})();

// ---------- network ----------
// The connection is KitLive (internal/livegame/tvlive.js), opened in boot.js.
// The TV is display-only: the host moves the night on from the console or the
// taproom iPad, never from this screen, which hangs on a wall with no input.
let netTimer = null;
function onHealth(down) { // only show the pill for an outage, not a blip
  clearTimeout(netTimer);
  if (!down) $("netdot").classList.remove("on"); else netTimer = setTimeout(() => $("netdot").classList.add("on"), 3000);
}

const player = id => S.players.find(p => p.id === id);
function esc(s) { return String(s).replace(/[&<>"]/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c])); }

// ---------- render ----------
function render(s) {
  S = s;

  const ev = s.events;
  if (ev.length) {
    const latest = ev[ev.length - 1];
    if (latest.seq > lastEventSeq) {
      const a = $("announcer");
      a.innerHTML = `<div class="new ${latest.text.length > 50 ? "long" : ""}">${esc(latest.text)}</div>`;
      a.classList.remove("flash"); void a.offsetWidth; a.classList.add("flash");
      lastEventSeq = latest.seq;
    }
  }

  $("lobby").classList.toggle("show", s.phase === HOP.phase.lobby);
  if (s.phase === HOP.phase.lobby) renderPond();
  renderChamp();
  if (s.phase === HOP.phase.final && lastPhase !== s.phase) fxConfetti(240);
  lastPhase = s.phase;
  document.body.classList.toggle("racing", s.phase === HOP.phase.racing && (s.sub === HOP.sub.flick || s.sub === HOP.sub.hop));

  $("info").textContent = s.phase === HOP.phase.racing && !s.final ? raceLabel() : "";
  const payHTML = s.phase === HOP.phase.racing && s.final ? `<div class="k-strip">The Final</div>` : "";
  if ($("pay").innerHTML !== payHTML) $("pay").innerHTML = payHTML;

  renderReady();
  renderTrack();
  renderTurn();
  renderResults();
}

// Words: Race / Heat / The Final. "Round" only when a round is split into heats.
const multiHeat = () => (S.heats || []).length > 1;
function raceLabel() {
  if (S.final) return "The Final";
  return multiHeat() ? `Round ${S.round} · ${S.heatName || "Heat " + S.heat}` : `Race ${S.round} of ${S.rounds}`;
}
// Where the night goes after these results (shown under the results and in the turn panel).
function nextLine() {
  if (S.roundOver && S.lastRace) return { head: "Last race done", next: "Final scores next" };
  const nx = nextHeat();
  if (nx) return { head: "", next: `Next: ${nx.final ? "The Final" : nx.name}` };
  if (S.roundOver) return { head: "Beer break", next: `Next: ${multiHeat() ? `Round ${S.round + 1}` : `Race ${S.round + 1} of ${S.rounds}`}` };
  return { head: "", next: "" };
}
const nameOf = id => player(id)?.name || "?";

// Short countdown before each race's first hop.
let readyKey = "", lastCount = -1;
function renderReady() {
  const r = $("ready");
  if (S.phase !== HOP.phase.racing || S.sub !== HOP.sub.ready) { r.classList.remove("show", "final"); readyKey = ""; return; }
  const key = S.round + "-" + S.heat;
  if (readyKey === key) return;
  readyKey = key; lastCount = -1;
  const first = player((S.turnOrder || [])[0]);
  const big = !!S.final;
  r.classList.toggle("final", big);
  r.innerHTML = big ? `<h3>The Final</h3><div class="n" id="readyN"></div>`
    : `<div class="kick">${esc(raceLabel())}</div><h3>Get ready</h3><div class="n" id="readyN"></div>${first ? `<p><b>${esc(first.name)}</b> goes first</p>` : ""}`;
  r.classList.remove("show"); void r.offsetWidth; r.classList.add("show");
  if (big) fxConfetti(120);
}

function renderPond() {
  const pond = $("pond"), n = S.players.length;
  const shown = S.players.filter(p => p.bot || p.connected), people = shown.filter(p => !p.bot).length;
  $("lobbyCount").textContent = people ? `${people} frog${people > 1 ? "s" : ""} in` : "";
  const html = shown.map((p, i) => {
    // spiral the pads around the pond
    const a = i * 2.4, r = 10 + 36 * Math.sqrt((i + 0.5) / Math.max(n, 1));
    const x = 50 + Math.cos(a) * r, y = 50 + Math.sin(a) * r * 0.8;
    const isNew = !pondSeen.has(p.id); pondSeen.add(p.id);
    return `<div class="pad ${p.connected ? "" : "off"} ${isNew ? "new" : ""}" style="left:${x}%;top:${y}%;animation-delay:${isNew ? "0s" : (-i * 0.4) + "s"}">
      ${frogSVG(p.color, p.hat)}<div class="nm">${esc(p.name)}${p.bot ? " 🤖" : ""}</div></div>`;
  }).join("");
  const key = S.players.map(p => p.id + p.color + p.hat + p.name + p.connected).join("|");
  if (pond.dataset.key === key) return;
  pond.dataset.key = key;
  pond.innerHTML = `<div class="ripple" style="left:30%;top:30%"></div><div class="ripple" style="left:70%;top:65%;animation-delay:-1.7s"></div>` + html;
}

// End of the night: the champion is simply the most points.
let champKey = "";
function renderChamp() {
  const on = S.phase === HOP.phase.final;
  $("champ").classList.toggle("show", on);
  if (!on) { champKey = ""; return; }
  const key = S.round + JSON.stringify(S.standings) + S.champion + S.players.map(p => p.color + p.hat + p.name).join();
  if (champKey === key) return;
  champKey = key;
  const champ = player(S.champion), st = (S.standings || []).filter(x => player(x.player)).slice(0, 8);
  const cpts = (S.standings || []).find(x => x.player === S.champion)?.points;
  $("champ").innerHTML = `<div class="kick">Tonight's winner</div>` +
    (champ ? `<div class="champ"><div class="hero k-cheer">${frogSVG(champ.color, champ.hat, { mood: "wow", badge: champ.wins > 0 })}</div>
      <div><div class="who k-wobble" id="champName">${esc(champ.name)}</div>${cpts != null ? `<div class="pts">${cpts} points</div>` : ""}</div></div>` : "") +
    `<div class="standings">${st.map((x, i) => {
      const p = player(x.player), top = x.rank === 1 && x.points > 0;
      return `<div class="k-row k-in-bounce ${top ? "is-first" : ""}" style="animation-delay:${(0.4 + i * 0.09).toFixed(2)}s"><span class="k-rank">${x.rank}</span>
        ${frogSVG(p.color, p.hat, { badge: !p.bot && p.wins > 0 })}<span class="k-name">${esc(p.name)}</span><span class="k-pts">${x.points}</span><span></span></div>`;
    }).join("")}</div>`;
  fitText($("champName"), 10, 5, innerWidth * 0.66);
}

// Shrink a one-line text until it fits (Bungee is wide), from max down to min vh.
// maxW: the width it may take (default: its own box, for a box with a fixed width).
function fitText(el, max, min, maxW) {
  if (!el) return;
  let fs = max;
  el.style.fontSize = fs + "vh";
  const room = () => maxW || el.clientWidth;
  while (fs > min && el.scrollWidth > room() + 1) { fs -= 0.2; el.style.fontSize = fs + "vh"; }
}
