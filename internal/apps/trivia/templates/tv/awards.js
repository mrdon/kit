/* Screen 8a: the honorable mentions. */
/* --- 8a. awards --- */

/* Its own PHASE, not a timed run-up inside the podium.

   The first version dealt the cards on a five second timer and then moved to
   the plinths by itself. In the room that was too fast to read and too slow
   to sit through, and it put the pace of the ending on a clock instead of on
   the person holding the microphone. Now the host presses once to put the
   mentions up, reads them out at whatever pace the room is going at, and
   presses again for the winner.

   The stagger below is the only thing left on a timer, and it is short: the
   cards land one after another so the screen has some life, and then the
   screen simply holds. Nothing here advances the game. */
var AWARD_DEAL_MS = 700;

function renderAwards(phaseChanged) {
  show('s-awards');
  var list = state.awards || [];
  var host = document.getElementById('awards');

  /* Already dealt, and no phase change: leave it exactly as it is. Frames
     land on this screen (a table rating the night, a reconnect) and the room
     is reading it -- re-dealing under them would be the bug the timed version
     had, in miniature. */
  if (!phaseChanged && host.childElementCount === list.length) { return; }

  host.innerHTML = '';
  list.forEach(function (a, i) {
    /* Everything is in the DOM immediately so a frame landing mid-deal
       repaints the whole list rather than a truncated one; the animation
       delay is what staggers them, not the insertion. */
    var card = awardCard(a);
    card.style.animationDelay = (i * AWARD_DEAL_MS) + 'ms';
    host.appendChild(card);
  });
}

/* The title carries whatever joke the award has and the line under it states
   the fact, which is the house copy rule rather than a layout accident: a
   straight line delivered straight is what makes the name funny. */
function awardCard(a) {
  var card = el('div', 'award');
  card.appendChild(el('div', 'award-title', a.title));
  card.appendChild(el('div', 'award-team', a.teamName));
  card.appendChild(el('div', 'award-detail', a.detail));
  return card;
}
