/* Screens 4/5: the cards -- reveal and betting -- built once per phase
   and diffed as chips land. */
/* --- 4/5. cards --- */

/* Twenty-one cards (twenty distinct answers plus "smaller than all of
   these") in the one row that suited five tables is 85px a card. So past
   seven the row splits, and the tier is a CLASS as much as a column count:
   the padding, the names, the chips and the footer all have to tighten
   together, and CSS says that better than JS does.

   Seven per row is the ceiling because that is roughly where a three-digit
   number stops fitting a card on a 1920 stage. Twenty-one is therefore
   three full rows; sixteen is three rows of six with two gaps at the end,
   which reads fine and is not worth extra markup to centre. */
function layoutCards(count) {
  var host = document.getElementById('cards');
  var screen = document.getElementById('cards-screen');
  var rows = count <= 7 ? 1 : (count <= 14 ? 2 : 3);
  var cols = Math.max(1, Math.ceil(count / rows));
  ['tier-1', 'tier-2', 'tier-3'].forEach(function (c) {
    host.classList.remove(c);
    screen.classList.remove(c);
  });
  host.classList.add('tier-' + rows);
  screen.classList.add('tier-' + rows);
  host.style.gridTemplateColumns = 'repeat(' + cols + ', minmax(0, 1fr))';
  host.style.gridTemplateRows = 'repeat(' + rows + ', minmax(0, 1fr))';
}

/* One card says "7" and the next says "1,000", and at twenty-one cards they
   share a 245px box -- so the size cannot be a constant any more than the
   board tile's could. Measure per card, because the cards are not even the
   same width as each other: the rail slides in during scoring and takes
   540px off the row. */
function fitCardValues() {
  var vals = document.querySelectorAll('#cards .card .val');
  var numeric = [], smallest = 999;
  for (var i = 0; i < vals.length; i++) {
    var v = vals[i];
    var pseudo = v.parentNode.classList.contains('pseudo');
    var size = fitOneValue(v, pseudo ? 40 : 120, pseudo ? 14 : 24, !pseudo);
    if (!pseudo) { numeric.push(v); smallest = Math.min(smallest, size); }
  }
  // One size for all of them. Fitting each card alone is correct and looks
  // wrong: a card whose answer three tables picked has two lines of names
  // and a smaller numeral than the card next to it, and a row of numerals
  // at six different sizes reads as a mistake rather than as information.
  for (var j = 0; j < numeric.length; j++) { numeric[j].style.fontSize = smallest + 'px'; }
}

/* Measured as a block: centred grid content overflows its box equally top
   and bottom and scrollHeight only counts the bottom half of that, so the
   loop would stop while half the numeral was still off the card. */
function fitOneValue(v, size, floor, nowrap) {
  v.style.display = 'block';
  // A numeral is measured on ONE line: with break-anywhere it would wrap
  // "1000000" into two lines that fit the height, and the loop would stop
  // at a size that reads as a hundred thousand and a zero.
  if (nowrap) { v.style.whiteSpace = 'nowrap'; }
  v.style.fontSize = size + 'px';
  while ((v.scrollHeight > v.clientHeight + 1 || v.scrollWidth > v.clientWidth + 1) && size > floor) {
    size -= 2;
    v.style.fontSize = size + 'px';
  }
  v.style.display = '';
  v.style.whiteSpace = '';
  return size;
}

/* --- 4/5. cards ---

   renderCards runs on EVERY frame, and during betting a frame arrives
   every time any table moves a chip. The first version rebuilt the deck
   each time -- innerHTML = '' and every card re-created with its `card-in`
   entrance -- so one table placing $100 made twenty cards flinch, and
   because the chips themselves were withheld from the wire back then,
   nothing else changed. That is exactly what "it flashes but nothing
   changes" was.

   So the deck is BUILT ONCE per (phase, round) and diffed after that: a
   chip that is new lands with its own animation, a chip that was lifted is
   removed, the pot and the tally are text. Nothing already on screen is
   touched, so nothing already on screen can re-animate. */
function renderCards(mode) {
  show('s-cards');
  var host = document.getElementById('cards');
  var key = mode + ':' + (state.round ? state.round.id : '');
  // 'scored' rebuilds every time: it is a phase change, and paintScored
  // wants a clean deck to dim, knock the losing chips off and re-mark the
  // winner.
  if (mode === 'scored' || host.dataset.builtFor !== key) {
    buildCards(host, mode);
    host.dataset.builtFor = key;
  } else {
    syncCards(host, mode);
  }
  cardsChrome(mode);
}

function showsChips(mode) { return mode === 'betting' || mode === 'scored'; }

function buildCards(host, mode) {
  host.className = 'cards';
  host.innerHTML = '';
  (state.slots || []).forEach(function (s, i) {
    var card = el('div', 'card' + (s.value === null ? ' pseudo' : ''));
    card.style.animationDelay = (i * 120) + 'ms';   // a stagger, so they land like dealt cards
    card.dataset.slotId = s.id;
    card.appendChild(el('div', 'val', s.label));
    // "and", not a middot: read the card out loud and that is what you say.
    card.appendChild(el('div', 'names', (s.teams || []).join(' and ')));
    var tray = el('div', 'tray');
    if (showsChips(mode)) {
      (s.chips || []).forEach(function (c, ci) { tray.appendChild(chipNode(c, ci * 60)); });
    }
    markCrowded(tray);
    card.appendChild(tray);
    // The pot line is always in the DOM, hidden while it is zero, so the
    // card does not change height the moment the first chip lands on it.
    card.appendChild(potNode(s, mode));
    host.appendChild(card);
  });
  layoutCards((state.slots || []).length);
}

function potNode(s, mode) {
  var pot = el('div', 'pot', money(s.pot || 0));
  pot.style.visibility = (showsChips(mode) && s.pot) ? '' : 'hidden';
  return pot;
}

/* The diff. Cards are matched by slot id, never by index, so a card node
   outlives every frame of the phase it was dealt in. */
function syncCards(host, mode) {
  (state.slots || []).forEach(function (s) {
    var card = host.querySelector('.card[data-slot-id="' + s.id + '"]');
    if (!card) { return; }
    if (showsChips(mode)) { syncChips(card.querySelector('.tray'), s.chips || []); }
    var pot = card.querySelector('.pot');
    if (!pot) { return; }
    pot.textContent = money(s.pot || 0);
    pot.style.visibility = (showsChips(mode) && s.pot) ? '' : 'hidden';
  });
}

/* A chip has no id on the wire, and it does not need one: a table has at
   most one chip per card (the DB says so), so team+amount identifies it.
   Counting rather than comparing lists means two tables with the same name
   still come out right. */
/* A popular answer at twenty tables can carry a dozen chips, and a dozen
   named pills stacked in one card climb straight out of it, over the value
   they are betting on. Past six the tray goes compact: amounts only, so
   the shape of the pile still reads and the pot underneath says what it
   adds up to. The names on a crowded card were never legible from the bar
   anyway. */
function markCrowded(tray) {
  tray.classList.toggle('crowded', tray.querySelectorAll('.chip').length > 6);
}

function chipKey(c) { return c.amount + '@' + (c.team || ''); }

function syncChips(tray, chips) {
  if (!tray) { return; }
  var want = {};
  chips.forEach(function (c) { var k = chipKey(c); want[k] = (want[k] || 0) + 1; });
  var have = tray.querySelectorAll('.chip');
  for (var i = have.length - 1; i >= 0; i--) {
    var k = have[i].dataset.chipKey;
    if (want[k]) { want[k]--; } else { tray.removeChild(have[i]); }   // lifted
  }
  // Whatever is still owed is genuinely new, so only those animate in.
  chips.forEach(function (c) {
    var k2 = chipKey(c);
    if (!want[k2]) { return; }
    want[k2]--;
    tray.appendChild(chipNode(c, 0));
  });
  markCrowded(tray);
}

/* The chip says WHOSE it is. A $200 disc told the room that money had
   moved and nothing about who was chasing what, which is half the fun of
   watching the betting -- and at 54px there is nowhere to put a name. So
   it is a pill: amount, then the table, clipped rather than wrapped so one
   long name cannot reflow a tray. */
function chipNode(c, delay) {
  var chip = el('div', 'chip ' + (c.amount >= 200 ? 'c200' : 'c100'));
  chip.dataset.chipKey = chipKey(c);
  chip.style.animationDelay = (delay || 0) + 'ms';
  chip.appendChild(el('b', 'amt', money(c.amount)));
  if (c.team) { chip.appendChild(el('span', 'who', c.team)); }
  return chip;
}

/* Everything around the deck: the question, the tally, the footer and the
   bits of the scoring beat that have to be put back to neutral. */
function cardsChrome(mode) {
  var band = document.getElementById('answer-band');
  band.classList.remove('in');
  band.classList.remove('shown');
  document.getElementById('rail').classList.remove('in');
  document.getElementById('cards-screen').classList.remove('railed');
  var q = document.getElementById('cards-question');
  q.textContent = state.round ? state.round.text : '';
  fitCardsQuestion(q);
  // The chips are on the cards as they land, but a table that has placed
  // both and a table that has not started look the same from the back of
  // the room, so the tally still answers "are we waiting on anyone?".
  var tally = document.getElementById('bet-tally');
  if (mode === 'betting') {
    // From the server, not from tokens.length: a final deals ONE chip while
    // tokens still lists two, so this used to demand a second chip that was
    // never coming and the tally sat at "0 OF 6" through the whole final.
    var want = state.chipsPerTable || 1;
    var eligible = state.teams.filter(function (t) { return t.eligible; });
    var inCount = eligible.filter(function (t) { return t.chipsPlaced >= want; }).length;
    tally.textContent = inCount + ' OF ' + eligible.length + ' TABLES IN';
    tally.style.display = '';
  } else {
    tally.style.display = 'none';
  }
  // The footer holds either the countdown or the answer band, never both.
  document.getElementById('cards-footer').classList.toggle('scored', mode === 'scored');
  fitCardValues();
  startRing('cards');
}

/* The question above the cards has a fixed band (its row is what the deck
   does not get), so a three-line question was clipped mid-sentence with no
   ellipsis. Shrink until it fits; the floor is where it stops reading from
   the bar, and a shorter question is the real fix below that. The starting
   size is whatever the tier's CSS set, so the loop only ever goes down. */
function fitCardsQuestion(q) {
  q.style.fontSize = '';
  var size = parseFloat(getComputedStyle(q).fontSize) || 44;
  var max = parseFloat(getComputedStyle(q).maxHeight) || 112;
  while (q.scrollHeight > max + 1 && size > 22) {
    size -= 2;
    q.style.fontSize = size + 'px';
  }
}
