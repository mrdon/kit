/* Screen 8: the ending. One screen, one press.

   The mentions are the part of the night people have the most fun with, so
   they get the wall: big cards, dealt three at a time, and if there are
   more than three the screen turns the page every few seconds and deals
   the next three. The podium sits above them, compact. The standings have
   been public all night and the room already knows who won; the plinths
   confirm it and the crown gets its confetti, but the winner is not the
   thing anyone is still waiting to find out.

   The mentions used to be a phase of their own ahead of the podium, with a
   second press for the winner. Going from the winner (on the standings) to
   the mentions and back to the winner read as a loop, so the two merged. */

/* The plinths rise one at a time, winner last, then the first page of
   mentions deals in. A page holds this many cards and turns on this clock;
   nothing here advances the game. */
var PODIUM_RISE_MS = 600;
var AWARD_DEAL_MS = 700;
var AWARDS_PER_PAGE = 3;
var AWARD_PAGE_MS = 9000;

var awardPage = 0;
var awardPageTimer = null;

function renderPodium(phaseChanged) {
  show('s-podium');
  if (phaseChanged) {
    awardPage = 0;
    if (awardPageTimer) { clearTimeout(awardPageTimer); awardPageTimer = null; }
  }
  renderPlinths(phaseChanged);
  renderMentions(phaseChanged);
}

function renderPlinths(phaseChanged) {
  var host = document.getElementById('podium');
  if (!phaseChanged && host.childElementCount) { return; }
  host.innerHTML = '';
  var teams = state.teams.slice().sort(function (a, b) { return b.score - a.score; }).slice(0, 3);
  // Ranks 3, 2, 1 bottom-up: the winner lands last.
  var order = [2, 1, 0].filter(function (i) { return teams[i]; });
  var placed = {};
  order.forEach(function (idx, n) {
    later(n * PODIUM_RISE_MS, function () {
      if (placed[idx]) { return; }
      placed[idx] = true;
      var t = teams[idx];
      // Competition rank, not list position: two tables on the same money
      // are both #1, and both get the crown. The plinth heights stay by
      // position so the layout is still three steps.
      var rank = 1 + teams.filter(function (o) { return o.score > t.score; }).length;
      var p = el('div', 'plinth p' + (idx + 1) + (rank === 1 ? ' tied-top' : ''));
      if (rank === 1) { p.appendChild(el('div', 'crown', '♛')); }
      p.appendChild(el('div', 'rank', '#' + rank));
      p.appendChild(el('div', 'name', t.name));
      p.appendChild(el('div', 'score', money(t.score)));
      // Column order left-to-right is 2nd, 1st, 3rd, so the winner is
      // centre stage regardless of the order they appear in.
      if (idx === 0) { host.insertBefore(p, host.children[1] || null); }
      else if (idx === 1) { host.insertBefore(p, host.firstChild); }
      else { host.appendChild(p); }
      if (rank === 1) { burstWaves(p, 2, 1100, { count: 22, dist: 160, spread: 320 }); }
    });
  });
}

/* The mentions, a page at a time.

   Everything on a page is in the DOM immediately so a frame landing mid-deal
   repaints the whole page rather than a truncated one; the animation delay is
   what staggers the cards, not the insertion. A frame that lands while a page
   is up (a table rating the night, a reconnect) leaves it exactly as it is:
   re-dealing under a room that is reading is the bug the first timed version
   had, in miniature. Only a phase change or the page clock redraws. */
function renderMentions(phaseChanged) {
  var list = state.awards || [];
  var host = document.getElementById('awards');
  var head = document.getElementById('awards-head');
  if (!list.length) {
    host.innerHTML = '';
    head.textContent = '';
    return;
  }
  var pages = Math.ceil(list.length / AWARDS_PER_PAGE);
  if (!phaseChanged && host.childElementCount && host.getAttribute('data-page') === String(awardPage)) { return; }
  dealMentionPage(list, pages);
}

function dealMentionPage(list, pages) {
  var host = document.getElementById('awards');
  var head = document.getElementById('awards-head');
  var start = awardPage * AWARDS_PER_PAGE;
  var page = list.slice(start, start + AWARDS_PER_PAGE);
  // The first page waits for the plinths; later pages turn on their own.
  var lead = awardPage === 0 ? PODIUM_RISE_MS * 3 : 0;
  host.setAttribute('data-page', String(awardPage));
  host.innerHTML = '';
  head.textContent = pages > 1
    ? 'Honorable mentions · ' + (awardPage + 1) + ' of ' + pages
    : 'Honorable mentions';
  page.forEach(function (a, i) {
    var card = awardCard(a);
    card.style.animationDelay = (lead + i * AWARD_DEAL_MS) + 'ms';
    host.appendChild(card);
  });
  fitAwards();
  if (pages > 1) {
    if (awardPageTimer) { clearTimeout(awardPageTimer); }
    awardPageTimer = setTimeout(function () {
      awardPageTimer = null;
      if (state.phase !== PHASE.PODIUM) { return; }
      awardPage = (awardPage + 1) % pages;
      dealMentionPage(state.awards || [], pages);
    }, lead + page.length * AWARD_DEAL_MS + AWARD_PAGE_MS);
  }
}

/* Measure and shrink, the same loop as fitRail. Three cards have the whole
   lower half of the wall; the team name is sized to fill what is actually
   there and everything else on the card is derived from it. The floor is
   where a name stops being legible from the bar. */
function fitAwards() {
  var host = document.getElementById('awards');
  var screen = document.getElementById('s-podium');
  if (!host || !screen || !host.childElementCount) { return; }
  var avail = screen.offsetHeight - host.offsetTop - 40;
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

/* One long name must not shrink everybody else's card. Each name gives up a
   little of its own type first, exactly as fitRailNames does for the
   standings; past the floor it is allowed to wrap, because a name nobody can
   read is worse than a tall card. */
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
