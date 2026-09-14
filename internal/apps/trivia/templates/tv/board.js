/* Screen 2: the board, and the flip that turns a tile into a question. */
/* --- 2. board --- */
function renderBoard(prev) {
  show('s-board');
  document.getElementById('gamename').style.visibility = '';
  var grid = document.getElementById('board-grid');
  var cols = 0, rows = 0;
  state.board.forEach(function (c) {
    cols = Math.max(cols, c.col + 1);
    rows = Math.max(rows, c.row + 1);
  });
  grid.style.gridTemplateColumns = 'repeat(' + Math.max(cols, 1) + ', 1fr)';
  grid.style.gridTemplateRows = '120px repeat(' + Math.max(rows, 1) + ', 1fr)';
  grid.innerHTML = '';

  var topics = [];
  state.board.forEach(function (c) { topics[c.col] = c.topic; });
  for (var i = 0; i < cols; i++) { grid.appendChild(el('div', 'cat', topics[i] || '')); }

  for (var r = 0; r < rows; r++) {
    for (var c2 = 0; c2 < cols; c2++) {
      var cell = state.board.filter(function (x) { return x.col === c2 && x.row === r; })[0];
      var d = el('div', 'cell' + (cell && cell.played ? ' played' : ''));
      d.dataset.cellId = cell ? cell.id : '';
      d.appendChild(el('span', 'val', cell ? money(cell.points) : ''));
      grid.appendChild(d);
    }
  }
  if (prev && prev.phase === 'scoring') { /* returning from a round: no flip */ }
  fitCellValues();
}

/* $500 and $1,000 are different widths in Bungee, and 88px of the latter
   overflows the tile. Measure and shrink rather than picking a size that
   happens to work for one of them. */
function fitCellValues() {
  var vals = document.querySelectorAll('#board-grid .cell .val');
  for (var i = 0; i < vals.length; i++) {
    var v = vals[i];
    var box = v.parentNode.clientWidth - 20;
    var size = 88;
    v.style.fontSize = size + 'px';
    while (v.scrollWidth > box && size > 28) {
      size -= 4;
      v.style.fontSize = size + 'px';
    }
  }
}

/* The FLIP: measure the picked tile, clone it, transform the clone to fill
   the stage, then swap in the question. This is what makes the screen feel
   like Jeopardy. */
function flipFrom(cellId, done) {
  var tile = document.querySelector('[data-cell-id="' + cellId + '"]');
  var clone = document.getElementById('flip');
  if (!tile || !clone) { done(); return; }
  var r = tile.getBoundingClientRect();
  var stage = document.getElementById('fit').getBoundingClientRect();
  var scale = stage.width / 1920 || 1;
  var x = (r.left - stage.left) / scale, y = (r.top - stage.top) / scale;
  var w = r.width / scale, h = r.height / scale;

  clone.style.transition = 'none';
  clone.style.left = x + 'px'; clone.style.top = y + 'px';
  clone.style.width = w + 'px'; clone.style.height = h + 'px';
  clone.style.transform = 'none';
  clone.style.opacity = '1';
  clone.innerHTML = tile.innerHTML;
  // Force a reflow so the transition below starts from the measured rect.
  void clone.offsetWidth;
  clone.style.transition = '';
  clone.style.transform =
    'translate(' + (64 - x) + 'px,' + (64 - y) + 'px) scale(' + ((1920 - 128) / w) + ',' + ((1080 - 128) / h) + ')';
  // Hiding the clone is CLEANUP, not choreography, so it goes on a plain
  // timeout rather than the cancellable one. Sharing `later` meant any
  // frame landing inside the 620ms cancelled it -- and during a question
  // the first table's answer nearly always does -- which left the blown-up
  // tile stuck over the whole screen until somebody reloaded the TV.
  setTimeout(function () { clone.style.opacity = '0'; }, 620);
  later(620, done);
}
