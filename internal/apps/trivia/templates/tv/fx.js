/* Celebration effects, shared by the scoring beat and the podium.

   It lives in its own file because two screens want it: the scoring beat
   sparkles the winning card, the podium showers the winner. Everything here
   is absolutely positioned divs on CSS transitions -- no library, no canvas,
   and it reads as celebration from thirty feet, which is the only test that
   matters on a wall. */

/* burst scatters sparks outward from a point inside `node`, which must be a
   positioned element (the sparks are absolute). Defaults are the podium's;
   a card wants fewer and shorter, which is what the options are for. */
function burst(node, opts) {
  opts = opts || {};
  var count = opts.count || 20;
  var near = opts.dist || 180;
  var far = opts.spread || 260;
  var top = opts.top || '30%';
  for (var i = 0; i < count; i++) {
    var s = el('div', 'spark' + (opts.small ? ' small' : ''));
    var angle = Math.random() * Math.PI * 2;
    var dist = near + Math.random() * far;
    s.style.left = '50%'; s.style.top = top;
    s.style.transition = 'transform ' + (700 + Math.random() * 600) + 'ms ease-out, opacity 1s';
    node.appendChild(s);
    (function (node2, angle2, dist2) {
      setTimeout(function () {
        node2.style.transform = 'translate(' + Math.cos(angle2) * dist2 + 'px,' + Math.sin(angle2) * dist2 + 'px)';
        node2.style.opacity = '0';
      }, 20);
    })(s, angle, dist);
  }
}

/* burstWaves is burst, repeated, so the winner's moment lasts as long as the
   room takes to notice it. One burst is over in a second -- by the time the
   bar has looked up from the podium rising, it has already finished.

   The timers go through `later`, so a frame landing mid-sequence cancels the
   remaining waves: the server always wins, here as everywhere. */
function burstWaves(node, waves, gapMs, opts) {
  for (var w = 0; w < waves; w++) {
    (function (n) {
      later(n * gapMs, function () { burst(node, opts); });
    })(w);
  }
}

/* sparkleOver puts a short burst over one card without letting it escape the
   deck's layout.

   A card has `overflow: hidden` (its names and chips are clipped to it on
   purpose) and is a grid item, so sparks appended to the card itself would be
   cut off at its edge. Instead an absolutely positioned layer is laid over
   the card inside `#cards`: absolute children take no part in grid layout, and
   offsetLeft/offsetTop are stage pixels rather than screen pixels, so this is
   immune to the 1920x1080 stage's scale transform. */
function sparkleOver(card) {
  var host = document.getElementById('cards');
  if (!host || !card) { return; }
  var fx = el('div', 'fx');
  fx.style.left = card.offsetLeft + 'px';
  fx.style.top = card.offsetTop + 'px';
  fx.style.width = card.offsetWidth + 'px';
  fx.style.height = card.offsetHeight + 'px';
  host.appendChild(fx);
  burst(fx, { count: 22, dist: 70, spread: 170, top: '45%', small: true });
  later(1800, function () { if (fx.parentNode) { fx.parentNode.removeChild(fx); } });
}
