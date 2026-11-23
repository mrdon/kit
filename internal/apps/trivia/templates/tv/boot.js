/* Boot: the clock, and the live connection (KitLive also polls the page's
   version and reloads it when it changes). Last on purpose -- these are
   statements, and they run once everything above is declared. */
/* ---------- clock ---------- */
setInterval(function () {
  var d = new Date();
  var h = d.getHours() % 12 || 12;
  var m = String(d.getMinutes()).padStart(2, '0');
  document.getElementById('clock').textContent = h + ':' + m;
}, 1000);

fit();
KitLive.open({
  stream: window.__KIT_TRIVIA_STREAM__,
  poll: window.__KIT_TRIVIA_POLL__,
  versionUrl: window.__KIT_TRIVIA_VERSION_URL__,
  version: window.__KIT_TRIVIA_VERSION__,
  onFrame: apply,
  onHealth: setDot
});
