// Renders the trivia OBS overlay against fake game states, over a fake
// football broadcast, to PNGs. Usage (from the repo root):
//   node docs/trivia-obs-overlay/preview.mjs [outdir] [scenario...]
// outdir defaults to dist/trivia-obs-overlay; sheet.png there tiles them all.
// Needs google-chrome and ImageMagick on the PATH. Dumps the page via TestDumpOverlayPreview.
import { execFileSync } from 'node:child_process';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';

const out = resolve(process.argv[2] || 'dist/trivia-obs-overlay');
mkdirSync(out, { recursive: true });
const only = process.argv.slice(3);
const work = join(out, 'html');
mkdirSync(work, { recursive: true });
execFileSync('go', ['test', '-run', 'TestDumpOverlayPreview', '-count=1', './internal/apps/trivia/'],
  { env: { ...process.env, TRIVIA_OVERLAY_DUMP: work }, stdio: 'inherit' });
const page = readFileSync(join(work, 'overlay.html'), 'utf8');

const teams = ['Bar Flies', 'Hop Heads', 'Trivia Newton-John', 'Quizzly Bears', 'The Know-Nothings',
  'Pint Sized', 'Les Quizerables', 'Brewski Bunch'].map((name, i) => ({
  id: 't' + i, name, score: [1400, 1100, 900, 2300, 600, 1800, 1250, 300][i], eligible: true,
}));
const topics = ['Pizza', 'Space', 'Beer', 'Football', 'Cats'];
const left = [3, 2, 4, 1, 0];
const board = topics.flatMap((topic, col) => [0, 1, 2, 3, 4].map(row => ({
  id: `c${col}${row}`, col, row, topic, points: (row + 1) * 100, played: row >= left[col],
})));
const round = { id: 'r1', ordinal: 7, points: 300, text: 'How many pizzerias are there in the United States?',
  category: 'Pizza', answered: 3, eligible: 8 };

function slots(values, pots, names) {
  const s = [{ id: 's0', pos: 0, value: null, label: 'Smaller than all', teams: [], pot: pots[0] || 0 }];
  values.forEach((v, i) => s.push({
    id: 's' + (i + 1), pos: i + 1, value: v, label: v.toLocaleString('en-US'),
    teams: names[i] ? [names[i]] : [], pot: pots[i + 1] || 0,
  }));
  return s;
}
const nine = [8000, 24000, 41000, 61500, 75000, 90000, 120000, 250000];
const nineNames = ['Bar Flies', 'Pint Sized', 'Quizzly Bears', 'Hop Heads', 'Les Quizerables',
  'Brewski Bunch', 'Trivia Newton-John', 'The Know-Nothings'];
const ninePots = [100, 200, 200, 600, 0, 100, 300, 0, 200];
const twenty = [1200, 5000, 8000, 12000, 24000, 30000, 41000, 52000, 61500, 70000, 75000, 78500,
  90000, 99000, 120000, 150000, 200000, 250000, 400000, 999999];
const twentyPots = [100, 0, 200, 600, 0, 100, 300, 0, 200, 0, 0, 200, 600, 0, 100, 300, 0, 200, 0, 0, 200];

const base = { version: 1, game: 'tuesday', title: 'Tuesday Quiz', teams, board, tokens: [100, 200] };
const T = 'DEADLINE';  // replaced in the page with Date.now() + secs
const scenarios = {
  lobby: { ...base, phase: 'lobby', board: [] },
  board: { ...base, phase: 'board', picker: { teamId: 't0', name: 'Bar Flies' }, pickerReason: 'wrote_winner' },
  question: { ...base, phase: 'question', round, deadlineMs: [T, 23] },
  reveal: { ...base, phase: 'reveal', round, deadlineMs: [T, 8], slots: slots(nine, [], nineNames) },
  betting: { ...base, phase: 'betting', round, deadlineMs: [T, 18], slots: slots(nine, ninePots, nineNames) },
  betting21: { ...base, phase: 'betting', round, deadlineMs: [T, 18], slots: slots(twenty, twentyPots, []) },
  scoring: { ...base, phase: 'scoring', round, slots: slots(nine, ninePots, nineNames),
    scoring: { correctValue: 78000, correctText: '78,000', winningSlot: 's5',
      deltas: { t4: 400, t6: 300, t1: -600, t5: -200 } } },
  wager: { ...base, phase: 'wager', round: { ...round, isFinal: true }, deadlineMs: [T, 41] },
  paused: { ...base, phase: 'question', round, pausedMs: 31000 },
  intermission: { ...base, phase: 'intermission' },
  podium: { ...base, phase: 'podium' },
};

const fakeGame = `<div style="position:absolute;left:480px;top:0;width:1440px;height:810px;z-index:-1;
  background:repeating-linear-gradient(90deg,#2e7d32 0 120px,#388e3c 120px 240px);font:bold 80px sans-serif;
  color:rgba(255,255,255,.4);display:flex;align-items:center;justify-content:center">
  <div style="position:absolute;left:40px;top:30px;background:rgba(0,0,0,.75);color:#fff;font:bold 34px sans-serif;
  padding:10px 22px;border-radius:6px">KC 17&nbsp;&nbsp; BUF 14&nbsp; <span style="color:#ffd600">Q3 4:12</span></div>
  THE GAME</div>`;

for (const [name, state] of Object.entries(scenarios)) {
  if (only.length && !only.includes(name)) { continue; }
  const json = JSON.stringify(state).replace(/\["DEADLINE",(\d+)\]/g, '(Date.now()+$1*1000-300)');
  const stub = `<script>
window.EventSource = function () {
  var self = this; this.readyState = 1; this.close = function () {};
  this.addEventListener = function (type, fn) {
    if (type !== 'state') { return; }
    setTimeout(function () {
      var s = JSON.parse(JSON.stringify(${json.replace(/"\(Date[^"]*\)"/g, m => m.slice(1, -1))}));
      s.serverNow = Date.now(); fn({ data: JSON.stringify(s) });
    }, 50);
  };
};
window.fetch = function () { return new Promise(function () {}); };
</script>
<style>.ov-main.fade{animation:none!important}</style>`;
  const html = page.replace('<head>', '<head>' + stub).replace('<div id="fit">', fakeGame + '<div id="fit">');
  const file = join(work, name + '.html');
  writeFileSync(file, html);
  const png = join(out, name + '.png');
  execFileSync('google-chrome', ['--headless=new', '--hide-scrollbars', '--force-device-scale-factor=1', '--window-size=1920,1167',
    '--virtual-time-budget=1500', '--screenshot=' + png, 'file://' + file], { stdio: 'ignore' });
  // Headless Chrome's viewport is the window less 87px of absent toolbar, so
  // the window is 1167 tall for a 1080 stage and the slack is cropped off.
  execFileSync('convert', [png, '-crop', '1920x1080+0+0', '+repage', png]);
  console.log(png);
}
const pngs = Object.keys(scenarios).map(n => join(out, n + '.png'));
execFileSync('montage', [...pngs, '-tile', '3x', '-geometry', '640x360+4+4', '-background', '#888',
  join(out, 'sheet.png')]);
console.log(join(out, 'sheet.png'));
