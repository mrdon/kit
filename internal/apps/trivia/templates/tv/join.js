/* Screen 1: the join screen -- QR, rules, and the pills as tables arrive. */
/* --- 1. join --- */
function renderJoin() {
  show('s-join');
  // The night's name is already the hero here; repeating it in the corner
  // is noise.
  document.getElementById('gamename').style.visibility = 'hidden';
  document.getElementById('join-count').textContent =
    state.teams.length + (state.teams.length === 1 ? ' TEAM IN' : ' TEAMS IN');
  var host = document.getElementById('join-pills');
  // Only append what is new, so existing pills keep their entrance and the
  // wall does not re-animate every pill each time somebody joins.
  var have = host.childElementCount;
  for (var i = have; i < state.teams.length; i++) {
    host.appendChild(el('div', 'pill', state.teams[i].name));
  }
  while (host.childElementCount > state.teams.length) { host.removeChild(host.lastChild); }
  // Pills first: how much bottom they claim is what is left for the column
  // beside the QR, and the three loops below all measure against it.
  fitJoinPills();
  fitJoinHero();
  fitJoinURL();
  fitJoinRules();
}

/* A full room is twenty pills, which at the five-table size is four rows
   deep -- and the strip only ever had 130px reserved under the grid, so the
   pills climbed up over the rules and under the QR code's quiet zone. That
   is the worst thing this screen can do: the QR is the only thing on it
   anybody has to act on.

   So: shrink the pills into a budget, then tell the grid what they actually
   took. 220px is the budget because the QR is 700px and the stage gives the
   join screen 952 -- anything more and the code starts shrinking. A room of
   five never enters the loop and looks exactly as it did. */
function fitJoinPills() {
  var host = document.getElementById('join-pills');
  var join = document.querySelector('.join');
  if (!host || !join) { return; }
  var size = 32;
  var apply = function (px) {
    host.style.setProperty('--pill-size', px + 'px');
    host.style.setProperty('--pill-pad-y', Math.round(px * 0.55) + 'px');
    host.style.setProperty('--pill-pad-x', Math.round(px * 0.86) + 'px');
    host.style.setProperty('--pill-gap', Math.max(8, Math.round(px * 0.4)) + 'px');
  };
  apply(size);
  // 16px is the floor: a table that cannot read its own name off the wall
  // has not really been told it is in.
  while (host.offsetHeight > 220 && size > 16) {
    size -= 2;
    apply(size);
  }
  join.style.setProperty('--pill-reserve', Math.max(130, host.offsetHeight + 40) + 'px');
}

/* The hero is the host's own words, so its size cannot be a constant: "Quiz
   night, 1 Sep" is four lines where "Trivia" is one. Measure and shrink
   until it fits the space it has, the same loop the menu board uses --
   clamp() guesses, a loop knows. */
function fitJoinHero() {
  var box = document.getElementById('join-words');
  if (!box) { return; }
  var words = box.querySelectorAll('.join-word');
  if (!words.length) { return; }
  /* The name is confirmation, not the headline -- the QR is. Two lines of
     it is plenty; shrink rather than let it push the rules off the screen. */
  var avail = 240;
  var size = 104;
  var apply = function (px) {
    for (var i = 0; i < words.length; i++) { words[i].style.fontSize = px + 'px'; }
  };
  apply(size);
  while ((box.scrollHeight > avail || box.scrollWidth > box.clientWidth) && size > 40) {
    size -= 4;
    apply(size);
  }
}

/* Five rules or six depending on whether the final is on, and the wording
   is fixed -- so the only variable is how much room is left beside the QR.
   Measure and shrink, with 22px as the floor: below that nobody reads it
   from the bar and the honest answer is that it does not fit. */
function fitJoinRules() {
  var list = document.getElementById('join-rules');
  if (!list) { return; }
  var col = list.parentNode;
  var size = 27;
  list.style.fontSize = size + 'px';
  while (col.scrollHeight > col.clientHeight && size > 22) {
    size -= 1;
    list.style.fontSize = size + 'px';
  }
}

/* Somebody is reading this off a wall and typing it into a phone, so it has
   to stay on one line. Shrink to fit; never hyphenate mid-word. */
function fitJoinURL() {
  var u = document.getElementById('join-url');
  if (!u) { return; }
  var box = u.parentNode.clientWidth;
  var size = 48;
  u.style.fontSize = size + 'px';
  while (u.scrollWidth > box && size > 24) {
    size -= 2;
    u.style.fontSize = size + 'px';
  }
}
