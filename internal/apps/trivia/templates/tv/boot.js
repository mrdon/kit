/* Boot: the version poll, the clock, and the first connect. Last on
   purpose -- these are statements, and they run once everything above is
   declared. */
setInterval(function () {
  fetch(window.__KIT_TRIVIA_VERSION_URL__, { credentials: 'same-origin' })
    .then(function (r) { return r.text(); })
    .then(function (v) {
      if (v && v.trim() && v.trim() !== window.__KIT_TRIVIA_VERSION__) {
        window.location.reload();
      }
    })
    .catch(function () { /* offline; try again next tick */ });
}, 15000);

/* ---------- clock ---------- */
setInterval(function () {
  var d = new Date();
  var h = d.getHours() % 12 || 12;
  var m = String(d.getMinutes()).padStart(2, '0');
  document.getElementById('clock').textContent = h + ':' + m;
}, 1000);

document.addEventListener('visibilitychange', function () {
  if (!document.hidden) { connect(); poll(); }
});

fit();
connect();
poll();
