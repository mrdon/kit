/* Screen 2a: the wheel that picks who chooses the first category.

   The host used to pick it, which is the one moment of the night where the
   quiz looks like it is playing itself. So the SERVER draws a table at random
   when the game starts, and this is the animation of a decision that has
   already been made -- which is the only way the wheel and the board can ever
   agree about who won.

   That inversion is the whole design: the final rotation is COMPUTED from the
   drawn team's wedge rather than the winner being read off wherever the wheel
   happens to stop. A wheel that decided anything would have to tell the
   server, over bar wifi, mid-animation. */

var WHEEL_SPIN_MS = 3500;   /* the spin itself, matched by the CSS transition */
var WHEEL_BAND_MS = 1800;   /* how long "X PICKS FIRST" holds before the board */
var WHEEL_TURNS = 5;        /* full revolutions before it lands */

/* One wheel per game, tracked so a frame landing mid-spin resumes the
   sequence rather than restarting it -- a table joining during the spin used
   to snap the wheel back to the top. */
var wheel = { game: '', picker: '', startedAt: 0, done: false };

/* Whether this board entry is the one that gets a wheel.
   It is, only when: the server says the picker was DRAWN (so this is the
   start of the night, not a pick that followed a round), nothing has been
   played yet, and this browser has not already watched it. */
function wheelIsDue() {
  if (!state || state.phase !== 'board') { return false; }
  if (state.pickerReason !== 'drawn' || !state.picker) { return false; }
  for (var i = 0; i < state.board.length; i++) {
    if (state.board[i].played) { return false; }
  }
  if (wheel.game === state.game && wheel.picker === state.picker.teamId) {
    // Already ours: still due while the sequence is running, so a frame
    // landing mid-spin resumes it -- and done once it has handed over to the
    // board, which is also what stops renderBoard and renderWheel calling
    // each other forever.
    return !wheel.done;
  }
  return !wheelSeen();
}

/* The once-per-game guard lives in sessionStorage rather than a variable,
   because the case it exists for is a RELOAD: a TV rebooted between the draw
   and the first cell should come up on the board with the callout, not spin
   a wheel the room already watched. Wrapped because a browser with storage
   blocked must still show a wheel, just a repeatable one. */
function wheelKey() { return 'kit-trivia-wheel:' + (state ? state.game : ''); }
function wheelSeen() {
  try { return window.sessionStorage.getItem(wheelKey()) === '1'; } catch (e) { return false; }
}
function markWheelSeen() {
  try { window.sessionStorage.setItem(wheelKey(), '1'); } catch (e) { /* private mode */ }
}

/* Names get smaller as the room gets bigger. Twenty tables is MaxTeams, and
   at twenty each wedge is 18 degrees, which is about as much text as a wedge
   can hold and still be read from the back of a bar. */
function wheelFontSize(n) {
  if (n <= 4) { return 46; }
  if (n <= 6) { return 40; }
  if (n <= 8) { return 34; }
  if (n <= 12) { return 28; }
  if (n <= 16) { return 23; }
  return 19;
}

function renderWheel() {
  show('s-wheel');
  document.getElementById('gamename').style.visibility = '';
  var teams = state.teams;
  var idx = 0;
  for (var i = 0; i < teams.length; i++) {
    if (teams[i].id === state.picker.teamId) { idx = i; }
  }

  var resuming = wheel.game === state.game && wheel.picker === state.picker.teamId;
  if (!resuming) {
    buildWheel(teams, idx);
    wheel.game = state.game;
    wheel.picker = state.picker.teamId;
    wheel.startedAt = Date.now();
    wheel.done = false;
  }

  /* Re-arm from where the spin actually is, not from zero. `apply` clears
     every choreography timer on each frame (the server always wins), so a
     join landing mid-spin arrives here with the CSS transition still running
     and the timers gone. */
  var elapsed = Date.now() - wheel.startedAt;
  var band = document.getElementById('wheel-band');
  if (elapsed >= WHEEL_SPIN_MS) {
    showWheelBand(band);
  } else {
    band.classList.remove('on');
    later(WHEEL_SPIN_MS - elapsed, function () { showWheelBand(band); });
  }
  later(Math.max(0, WHEEL_SPIN_MS + WHEEL_BAND_MS - elapsed), function () {
    wheel.done = true;
    markWheelSeen();
    renderBoard(null);
  });
}

function showWheelBand(band) {
  band.innerHTML = '';
  band.appendChild(el('span', 'wheel-band-name', state.picker.name));
  band.appendChild(el('span', 'wheel-band-tail', 'picks first'));
  band.classList.add('on');
  fitWheelBand();
}

/* Build the disc: a wedge per table as one conic-gradient, the names laid
   round the rim, and the landing rotation computed from the drawn team's
   index. Wedge i occupies [i*slice, (i+1)*slice) measured CLOCKWISE from the
   pointer at twelve o'clock, so bringing its centre under the pointer means
   turning back by its centre angle -- plus whole turns for the show. */
function buildWheel(teams, idx) {
  var disc = document.getElementById('wheel-disc');
  var slice = 360 / Math.max(teams.length, 1);
  var stops = [];
  var labels = document.getElementById('wheel-labels');
  labels.innerHTML = '';
  var size = wheelFontSize(teams.length);

  /* The landing is needed BEFORE the names are laid out, because each name is
     counter-rotated by its seat angle PLUS the landing: the labels layer turns
     with the disc, so cancelling only the seat leaves every name tilted by the
     landing angle once it stops. Cancelling both means the wheel comes to rest
     with all six names the right way up and the winner's -- the one name
     anybody is looking at -- squarely under the pointer. */
  var landing = WHEEL_TURNS * 360 - (idx * slice + slice / 2);

  for (var i = 0; i < teams.length; i++) {
    var from = i * slice, to = (i + 1) * slice;
    stops.push(wheelColour(i, teams.length) + ' ' + from + 'deg ' + to + 'deg');
    var seat = el('div', 'wheel-seat');
    seat.style.transform = 'rotate(' + (from + slice / 2) + 'deg)';
    var name = el('div', 'wheel-name', teams[i].name);
    name.style.fontSize = size + 'px';
    /* The arc a wedge gives a name shrinks as the room grows, so the clamp
       has to as well -- a fixed max-width let twenty names overlap into an
       unreadable ring. */
    name.style.maxWidth = Math.max(76, Math.min(240, Math.round(1500 / teams.length))) + 'px';
    name.style.transform = 'rotate(' + (-(from + slice / 2) - landing) + 'deg)';
    seat.appendChild(name);
    labels.appendChild(seat);
  }
  disc.style.background = 'conic-gradient(from 0deg, ' + stops.join(', ') + ')';

  disc.style.transition = 'none';
  labels.style.transition = 'none';
  setWheelRotation(0);
  void disc.offsetWidth;     // reflow, so the transition starts from zero
  disc.style.transition = '';
  labels.style.transition = '';
  setWheelRotation(landing);
  document.getElementById('wheel-band').classList.remove('on');
}

function setWheelRotation(deg) {
  document.getElementById('wheel-disc').style.transform = 'rotate(' + deg + 'deg)';
  document.getElementById('wheel-labels').style.transform = 'rotate(' + deg + 'deg)';
}

/* Two alternating wedge colours, with a third inserted when the count is odd
   so the first and last wedges do not meet in the same colour and read as one
   double-width wedge. */
function wheelColour(i, n) {
  var pair = ['#1b3a4b', '#e8543a'];
  if (n % 2 === 1 && i === n - 1) { return '#2f5a6e'; }
  return pair[i % 2];
}

/* The band names a table, and a table's name is whatever somebody typed into
   a phone. Measure and shrink rather than trusting a size. */
function fitWheelBand() {
  var name = document.querySelector('#wheel-band .wheel-band-name');
  if (!name) { return; }
  var size = 130;
  name.style.fontSize = size + 'px';
  while (name.scrollWidth > 1600 && size > 40) {
    size -= 6;
    name.style.fontSize = size + 'px';
    /* The arc a wedge gives a name shrinks as the room grows, so the clamp
       has to as well -- a fixed max-width let twenty names overlap into an
       unreadable ring. */
    name.style.maxWidth = Math.max(92, Math.min(230, Math.round(1400 / teams.length))) + 'px';
  }
}
