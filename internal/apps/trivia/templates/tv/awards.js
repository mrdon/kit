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
       delay is what staggers them, not the insertion. It also means the fit
       below measures the finished list: the landing animation moves the
       cards with a transform, which does not touch layout. */
    var card = awardCard(a);
    card.style.animationDelay = (i * AWARD_DEAL_MS) + 'ms';
    host.appendChild(card);
  });
  fitAwards();
}

/* Measure and shrink, the same loop as fitRail. Three mentions have room to
   be enormous and five do not, and one fixed size suits neither -- so the
   team name is sized to fill what is actually there and everything else on
   the card is derived from it. The floor is where a name stops being legible
   from the bar, and clipping the last card is the more honest failure past
   that. */
function fitAwards() {
  var host = document.getElementById('awards');
  var screen = document.getElementById('s-awards');
  if (!host || !screen || !host.childElementCount) { return; }
  var avail = screen.offsetHeight - host.offsetTop - 40;
  /* Start at the CEILING and come down, so the size is set by how much room
     there is rather than by a number that happened to suit five cards. Three
     mentions fill the wall; five settle wherever they fit. */
  var size = 110;
  var apply = function (px) {
    host.style.setProperty('--award-name', px + 'px');
    host.style.setProperty('--award-title', Math.round(px * 0.5) + 'px');
    host.style.setProperty('--award-detail', Math.round(px * 0.44) + 'px');
    host.style.setProperty('--award-pad', Math.round(px * 0.28) + 'px');
    host.style.setProperty('--award-gap', Math.round(px * 0.2) + 'px');
  };
  apply(size);
  fitAwardNames(size);
  while (host.scrollHeight > avail && size > 34) {
    size -= 2;
    apply(size);
    // Re-fit the names at every step: the card size and the name size are
    // not independent, and measuring the stack against names left at the
    // previous size overshoots.
    fitAwardNames(size);
  }
}

/* One long name must not shrink everybody else's card.

   "The Quizzards of Oz" is a normal team name and "Norwegian Wood
   Appreciation Society" is not an unusual one. Left to wrap, a single long
   name makes its card two lines taller, and the fit loop above then takes
   that height out of every OTHER card on the screen -- so one table's long
   name costs the other four their size. So each name gives up a little of
   its own type first, exactly as fitRailNames does for the standings.

   Past the floor it is allowed to wrap after all. A name nobody can read is
   worse than a tall card, and truncating somebody's table name on the one
   screen that exists to say it out loud is not on the table. */
function fitAwardNames(base) {
  var names = document.querySelectorAll('#awards .award-team');
  var floor = Math.max(30, Math.round(base * 0.55));
  for (var i = 0; i < names.length; i++) {
    var n = names[i];
    var size = base;
    n.style.whiteSpace = 'nowrap';
    n.style.fontSize = size + 'px';
    while (n.scrollWidth > n.clientWidth && size > floor) {
      size -= 2;
      n.style.fontSize = size + 'px';
    }
    if (n.scrollWidth > n.clientWidth) {
      // Still too wide at the floor: let it wrap and take the height.
      n.style.whiteSpace = 'normal';
    }
  }
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
