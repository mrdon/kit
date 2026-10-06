/* The events screen's driver.

   reveal.js owns the stage (a fixed 1920x1080 canvas scaled to the panel),
   the loop and the transitions between slides. What it leaves to a presenter
   -- when to move on, and tidying a slide away before it goes -- is here.

   Its own autoSlide is off on purpose. It advances on a timer with no hook
   BEFORE the slide leaves, and the exit choreography needs one: each slide
   sinks its contents out in reading order, and only then asks reveal for the
   next. Everything else is CSS keyed off the classes reveal already sets, so
   this script creates no DOM and no per-cycle state -- the page is meant to
   run for days, and nothing here accumulates. */
(function () {
  'use strict';

  var DEFAULT_DWELL = 14000;
  /* Leaving: a base for the last item's own animation, plus the stagger. It
     is capped so a seven-row week does not hold an empty slide on screen. */
  var EXIT_BASE = 560, EXIT_STEP = 55, EXIT_MAX = 1300;
  var VERSION_POLL = 5 * 60 * 1000;
  /* A reload a day regardless, as housekeeping: a browser left on one page
     for weeks is the one thing no amount of care in this file controls. */
  var MAX_AGE = 24 * 60 * 60 * 1000;

  /* ?hold stops the rotation where it is, for checking one slide on the
     wall with the arrow keys instead of chasing it round the loop. */
  var HOLD = /[?&]hold\b/.test(location.search);

  var root = document.querySelector('.reveal');
  var bar = document.getElementById('bar');
  var booted = Date.now();
  var stale = false;
  var timer = null;
  var barAnim = null;

  Reveal.initialize({
    width: 1920, height: 1080, margin: 0, minScale: 0.1, maxScale: 4,
    center: false, display: 'flex',
    controls: false, progress: false, slideNumber: false, help: false,
    hash: false, history: false, overview: false, touch: false,
    mouseWheel: false, jumpToSlide: false, pause: false,
    /* Arrows still work, so someone checking the screen can step through
       it; the pacing picks up again from wherever they leave it. */
    keyboard: true,
    loop: true,
    transition: 'fade', transitionSpeed: 'slow', backgroundTransition: 'fade',
    autoAnimateDuration: 1.1, autoAnimateEasing: 'cubic-bezier(.2,.7,.2,1)',
    autoSlide: 0,
    viewDistance: 3,
    hideInactiveCursor: true, hideCursorTime: 500,
    focusBodyOnPageVisibilityChange: false
  }).then(function () {
    tickClock();
    setInterval(tickClock, 15000);
    setInterval(checkVersion, VERSION_POLL);
    arrive(Reveal.getCurrentSlide());
  });

  Reveal.on('slidechanged', function (e) {
    if (e.previousSlide) { e.previousSlide.classList.remove('is-leaving'); }
    arrive(e.currentSlide);
  });

  function dwellOf(slide) {
    return parseInt(slide.getAttribute('data-dwell'), 10) || DEFAULT_DWELL;
  }

  /* A slide has landed: start its clock and the bar that shows it. */
  function arrive(slide) {
    clearTimeout(timer);
    var dwell = dwellOf(slide);
    if (HOLD) { return; }
    runBar(dwell);
    timer = setTimeout(function () { depart(slide); }, dwell);
  }

  /* Time is up: sink this slide's contents, then move on. A reload waiting
     on a new version happens here too, in the gap between slides, where a
     repaint reads as one more transition rather than as a glitch. */
  function depart(slide) {
    var single = Reveal.getTotalSlides() < 2;
    if (!single) { slide.classList.add('is-leaving'); }
    var items = slide.querySelectorAll('[data-in]').length;
    var wait = single ? 0 : Math.min(EXIT_MAX, EXIT_BASE + items * EXIT_STEP);
    timer = setTimeout(function () {
      if (stale) { location.reload(); return; }
      if (single) { arrive(slide); return; }
      Reveal.next();
    }, wait);
  }

  /* One animation on one element, cancelled before the next starts, so
     nothing piles up however long the loop runs. */
  function runBar(ms) {
    if (!bar.animate) { return; }
    if (barAnim) { barAnim.cancel(); }
    barAnim = bar.animate(
      [{ transform: 'scaleX(0)' }, { transform: 'scaleX(1)' }],
      { duration: ms, easing: 'linear', fill: 'forwards' });
  }

  /* The clock is the screen's pulse: someone walking past can tell a
     frozen screen from a quiet one. */
  var DAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
  function tickClock() {
    var d = new Date(), h = d.getHours(), m = d.getMinutes();
    var ampm = h >= 12 ? 'pm' : 'am';
    h = h % 12 || 12;
    var text = DAYS[d.getDay()] + ' ' + h + ':' + (m < 10 ? '0' : '') + m + ' ' + ampm;
    var clocks = document.querySelectorAll('[data-clock]');
    for (var i = 0; i < clocks.length; i++) { clocks[i].textContent = text; }
  }

  /* Ask whether the programme moved. The stamp includes today's date, so
     this is also how the screen turns over to the next events at midnight.
     Staleness is only ever set on a successful answer: a reload is never
     attempted while the server is unreachable, because that would swap a
     working screen for the browser's error page. */
  function checkVersion() {
    var url = location.pathname.replace(/\/$/, '') + '.version';
    fetch(url, { cache: 'no-store' })
      .then(function (r) { return r.ok ? r.text() : null; })
      .then(function (v) {
        if (!v) { return; }
        if (v !== root.getAttribute('data-version') || Date.now() - booted > MAX_AGE) {
          stale = true;
        }
      })
      .catch(function () { /* keep showing what we have */ });
  }
})();
