/* Screen 3b: the break between boards. */
/* --- the break between boards --- */
/* Standings, and what the room is coming back to. No clock: the whole point
   of the phase is that the night stops for a minute, and a countdown on the
   wall would put it straight back on. */
function renderBreak() {
  show('s-break');
  var next = document.getElementById('break-next');
  /* boardRound is ALREADY one-based on the wire, and it already points at the
     round about to be played -- the break is entered by crossing into it. */
  /* The frame already carries the NEXT round's cells and chips, so the wall
     names the real numbers instead of asserting a multiple. */
  var cell = state.board && state.board[0] ? money(state.board[0].points) + ' a cell · ' : '';
  var chips = (state.tokens || []).map(function (t) { return money(t); }).join(' / ');
  next.textContent = 'Round ' + state.boardRound + ' of ' + state.boardRounds +
    ' is next — ' + cell + 'chips ' + chips;

  var host = document.getElementById('break-scores');
  host.innerHTML = '';
  var teams = state.teams.slice().sort(function (a, b) { return b.score - a.score; });
  teams.forEach(function (t) {
    /* Competition rank, same as the podium: two tables on the same money are
       both in the same place, and the room will say so if the screen does
       not. */
    var rank = 1 + teams.filter(function (o) { return o.score > t.score; }).length;
    var row = el('li', 'breakrow');
    row.appendChild(el('span', 'brank', '#' + rank));
    row.appendChild(el('span', 'bname', t.name));
    row.appendChild(el('span', 'bscore', money(t.score)));
    host.appendChild(row);
  });
}
