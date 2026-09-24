/* Screen 8a: the honorable mentions, and the run-up to the podium. */
/* --- 8a. awards --- */

/* Awards are not a phase. They run INSIDE podium, ahead of the plinths,
   which is the same bargain the final made when it re-entered `question`
   with a boolean rather than growing four phases of its own. The host says
   "before we announce the winner" and presses the finish button they already
   have; everything below is the TV's own choreography behind that one click.

   awardStep is how many cards have landed. It is module state rather than a
   local because EVERY incoming frame calls clearTimers() and re-renders, so
   a keepalive arriving four cards in must resume from the fifth rather than
   replay the whole run at the room. */
var awardStep = 0;
var awardsOver = false;

/* Five seconds a card. The host is reading these out over the top of them,
   and a bar needs a beat to react before the next one lands. Five of them is
   half a minute, which is the right length for the lull before a podium and
   too long for anything else -- which is why nothing else uses this pacing. */
var AWARD_BEAT = 5000;

function renderAwards() {
  show('s-awards');
  var list = state.awards || [];
  var host = document.getElementById('awards');
  // Repaint what has already landed without re-animating it, then schedule
  // only what has not.
  if (host.childElementCount > awardStep) { host.innerHTML = ''; }
  for (var i = host.childElementCount; i < awardStep; i++) {
    host.appendChild(awardCard(list[i], false));
  }
  scheduleAwards(list);
}

function scheduleAwards(list) {
  var from = awardStep;
  var host = document.getElementById('awards');
  for (var i = from; i < list.length; i++) {
    (function (idx) {
      later((idx - from + 1) * AWARD_BEAT, function () {
        awardStep = idx + 1;
        host.appendChild(awardCard(list[idx], true));
      });
    })(i);
  }
  // One extra beat after the last card, so the final mention is read rather
  // than swept off by the plinths rising under it.
  later((list.length - from + 1) * AWARD_BEAT, function () {
    awardsOver = true;
    renderPlinths(true);
  });
}

/* The title carries whatever joke the award has and the line under it states
   the fact, which is the house copy rule rather than a layout accident: a
   straight line delivered straight is what makes the name funny. */
function awardCard(a, animate) {
  var card = el('div', 'award' + (animate ? ' landing' : ''));
  card.appendChild(el('div', 'award-title', a.title));
  card.appendChild(el('div', 'award-team', a.teamName));
  card.appendChild(el('div', 'award-detail', a.detail));
  return card;
}
