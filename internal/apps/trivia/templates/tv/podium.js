/* Screen 8: the podium. */
/* --- 8. podium --- */
function renderPodium(phaseChanged) {
  show('s-podium');
  var host = document.getElementById('podium');
  if (!phaseChanged && host.childElementCount) { return; }
  host.innerHTML = '';
  var teams = state.teams.slice().sort(function (a, b) { return b.score - a.score; }).slice(0, 3);
  // Ranks 3, 2, 1 bottom-up: the winner lands last.
  var order = [2, 1, 0].filter(function (i) { return teams[i]; });
  var placed = {};
  order.forEach(function (idx, n) {
    later(n * 900, function () {
      if (placed[idx]) { return; }
      placed[idx] = true;
      var t = teams[idx];
      var p = el('div', 'plinth p' + (idx + 1));
      if (idx === 0) { p.appendChild(el('div', 'crown', '♛')); }
      p.appendChild(el('div', 'rank', '#' + (idx + 1)));
      p.appendChild(el('div', 'name', t.name));
      p.appendChild(el('div', 'score', money(t.score)));
      // Column order left-to-right is 2nd, 1st, 3rd, so the winner is
      // centre stage regardless of the order they appear in.
      if (idx === 0) { host.insertBefore(p, host.children[1] || null); }
      else if (idx === 1) { host.insertBefore(p, host.firstChild); }
      else { host.appendChild(p); }
      // Three waves over roughly four seconds, not one. A single burst is
      // over before the bar has finished looking up from the plinth rising,
      // and the winner's moment is the only thing left on the wall -- there
      // is nothing for it to be competing with.
      if (idx === 0) { burstWaves(p, 3, 1300, { count: 26, dist: 200, spread: 380 }); }
    });
  });
}

/* The version poll, same shape as the menu board's.
   The SSE stream carries live state, but the QR code, the join words and
   the heading are baked into the HTML at render time -- so a renamed night,
   or a newer game appearing on the stable address, leaves the screen
   showing something wrong until somebody walks over to it. A few bytes
   every 15s, and a reload only when they actually change. */
