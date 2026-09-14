// Confetti, hand-rolled on a canvas.
//
// No library: this is one screen that fires at most a handful of times a
// night, and every confetti package on npm is 10-30kB of bundle on a phone
// that is already loading over bar wifi. A hundred lines of canvas is cheaper
// than the dependency and does not have opinions about React.
//
// The canvas is a fixed, pointer-events-none layer appended to <body> rather
// than a React node, so nothing it does can re-render the screen underneath
// it — the count-up on the delta is running at the same time and must not
// share a render loop with 120 falling rectangles.

// The phone's palette, so the confetti reads as the same product as the
// screen it is falling over.
const COLORS = ['#f5c451', '#e8543a', '#4ec9a5', '#ffb4a3', '#f4efe7'];

export interface ConfettiOptions {
  /** How long pieces keep falling, ms. The layer removes itself after. */
  durationMs?: number;
  /** How many pieces. 120 is the full "you won the game" shower. */
  count?: number;
}

interface Piece {
  x: number; y: number;      // px, stage coords
  vx: number; vy: number;    // px per second
  rot: number; vr: number;   // radians, radians per second
  w: number; h: number;
  color: string;
}

/**
 * confetti drops a burst over the whole viewport and returns a cancel
 * function. Calling cancel (or letting the duration run out) removes the
 * layer; it is safe to call twice.
 *
 * Under `prefers-reduced-motion` there is no motion at all: the same pieces
 * are painted once where they would have landed and the whole layer fades.
 * A player who asked the OS for less movement still gets told they won.
 */
export function confetti(opts: ConfettiOptions = {}): () => void {
  const duration = opts.durationMs ?? 2400;
  const count = opts.count ?? 90;

  const canvas = document.createElement('canvas');
  canvas.className = 'confetti-layer';
  const w = window.innerWidth;
  const h = window.innerHeight;
  // Cap the DPR at 2: a 3x phone would paint nine times the pixels for
  // rectangles nobody is inspecting.
  const dpr = Math.min(2, window.devicePixelRatio || 1);
  canvas.width = Math.floor(w * dpr);
  canvas.height = Math.floor(h * dpr);
  const ctx = canvas.getContext('2d');
  if (!ctx) return () => { /* no canvas, no celebration; not worth throwing */ };
  ctx.scale(dpr, dpr);
  document.body.appendChild(canvas);

  const pieces = spawn(count, w, h);
  let done = false;
  let raf = 0;
  const stop = () => {
    if (done) return;
    done = true;
    cancelAnimationFrame(raf);
    canvas.remove();
  };

  if (reduceMotion()) {
    staticFlash(ctx, pieces, w, h);
    canvas.style.transition = 'opacity .7s ease-out';
    // Two frames, because setting the transition and the target value in the
    // same frame gives you a jump cut rather than a fade.
    requestAnimationFrame(() => requestAnimationFrame(() => { canvas.style.opacity = '0'; }));
    const t = window.setTimeout(stop, 1100);
    return () => { clearTimeout(t); stop(); };
  }

  let last = performance.now();
  const started = last;
  const step = (now: number) => {
    const dt = Math.min(0.05, (now - last) / 1000);   // a backgrounded tab must not teleport
    last = now;
    const age = now - started;
    if (age >= duration) { stop(); return; }
    // Fade the last 700ms rather than cutting: pieces vanishing mid-air is
    // the one thing that reads as a bug rather than as an ending.
    ctx.clearRect(0, 0, w, h);
    ctx.globalAlpha = Math.min(1, (duration - age) / 700);
    for (const p of pieces) {
      p.vy += 420 * dt;                    // gravity
      p.x += p.vx * dt;
      p.y += p.vy * dt;
      p.rot += p.vr * dt;
      if (p.y > h + 40) { recycle(p, w); } // keep the shower going for the whole duration
      paint(ctx, p);
    }
    raf = requestAnimationFrame(step);
  };
  raf = requestAnimationFrame(step);
  return stop;
}

function reduceMotion(): boolean {
  try {
    return window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  } catch {
    return false;
  }
}

function spawn(count: number, w: number, h: number): Piece[] {
  const out: Piece[] = [];
  for (let i = 0; i < count; i++) {
    const p = { x: 0, y: 0, vx: 0, vy: 0, rot: 0, vr: 0, w: 0, h: 0, color: '' };
    recycle(p, w);
    // Stagger the start heights over two screens so the shower arrives as a
    // stream instead of as one horizontal line of rectangles.
    p.y = -Math.random() * h * 1.6 - 20;
    out.push(p);
  }
  return out;
}

function recycle(p: Piece, w: number) {
  p.x = Math.random() * w;
  p.y = -30;
  p.vx = (Math.random() - 0.5) * 120;
  p.vy = 60 + Math.random() * 200;
  p.rot = Math.random() * Math.PI * 2;
  p.vr = (Math.random() - 0.5) * 10;
  p.w = 6 + Math.random() * 7;
  p.h = 9 + Math.random() * 8;
  p.color = COLORS[Math.floor(Math.random() * COLORS.length)];
}

function paint(ctx: CanvasRenderingContext2D, p: Piece) {
  ctx.save();
  ctx.translate(p.x, p.y);
  ctx.rotate(p.rot);
  ctx.fillStyle = p.color;
  ctx.fillRect(-p.w / 2, -p.h / 2, p.w, p.h);
  ctx.restore();
}

// The reduced-motion fallback: the pieces scattered down the screen, painted
// once and never moved.
function staticFlash(ctx: CanvasRenderingContext2D, pieces: Piece[], w: number, h: number) {
  const wash = ctx.createLinearGradient(0, 0, 0, h);
  wash.addColorStop(0, 'rgba(245,196,81,.30)');
  wash.addColorStop(1, 'rgba(245,196,81,0)');
  ctx.fillStyle = wash;
  ctx.fillRect(0, 0, w, h);
  for (const p of pieces) {
    p.y = Math.random() * h;
    paint(ctx, p);
  }
}
