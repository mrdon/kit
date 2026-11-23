// The frogs' idle animations and the page effects (pops, puffs, confetti),
// plus the random frog names. See frog.js for how these files are shared.

import { FROG_RIG_CSS } from './frog.js';

// Shared whimsy: idle animations + effects. Injected once per page.
(function injectFrogCSS() {
  const css = `
  .frogsvg { overflow: visible; filter: url(#ink0); animation: ink-boil 0.45s steps(1) infinite; }
  @keyframes ink-boil { 0% { filter: url(#ink0); } 33% { filter: url(#ink1); } 66% { filter: url(#ink2); } }
  .frogsvg .lid { transform-box: fill-box; transform-origin: 50% 0%; animation: frog-blink 4.6s infinite; animation-delay: var(--blink); }
  .fly { position: absolute; width: 34%; pointer-events: none; z-index: 12; animation: fly-buzz var(--fd, 2.2s) ease-in-out infinite; animation-delay: var(--fdl, 0s); }
  .fly svg { width: 100%; overflow: visible; filter: url(#ink1); }
  .fly .wings { animation: fly-wings 0.08s steps(2) infinite; transform-origin: 50% 80%; transform-box: fill-box; }
  @keyframes fly-wings { 50% { transform: scaleY(0.4); } }
  @keyframes fly-buzz {
    0% { transform: translate(-60%, -40%) rotate(-10deg); } 20% { transform: translate(90%, -120%) rotate(15deg); }
    40% { transform: translate(180%, -30%) rotate(-5deg); } 55% { transform: translate(120%, -160%) rotate(20deg); }
    75% { transform: translate(-20%, -150%) rotate(-20deg); } 100% { transform: translate(-60%, -40%) rotate(-10deg); } }
  .frogsvg .torso { transform-box: fill-box; transform-origin: 50% 100%; animation: frog-breathe 2.4s ease-in-out infinite; animation-delay: calc(var(--breathe) * -1); }
  .frogsvg .pupils { animation: frog-look 7s ease-in-out infinite; animation-delay: calc(var(--blink) * -1.3); }
  .frogsvg .hat { transform-box: view-box; transform-origin: 0 0; animation: frog-hat 2.4s ease-in-out infinite; animation-delay: calc(var(--breathe) * -1); }
  @keyframes frog-breathe { 0%,100% { transform: scale(1, 1); } 50% { transform: scale(1.035, 0.965); } }
  @keyframes frog-blink { 0%, 92%, 100% { transform: scaleY(1); } 95% { transform: scaleY(2.05); } }
  @keyframes frog-look { 0%,40%,100% { transform: translate(0,0); } 50%,60% { transform: translate(-2px,0); } 70%,80% { transform: translate(2px,-1px); } }
  @keyframes frog-hat { 0%,100% { transform: rotate(-1.5deg); } 50% { transform: rotate(1.5deg) translateY(-0.4px); } }
  .fx-pop { position: fixed; pointer-events: none; z-index: 50; font-family: "Baloo 2", system-ui, sans-serif; font-weight: 800;
    color: #fff3c4; -webkit-text-stroke: 2px #6b2d00; paint-order: stroke fill; text-shadow: 0 4px 0 #6b2d00;
    animation: fx-pop 1.1s cubic-bezier(.2,1.6,.4,1) forwards; transform: translate(-50%, -50%); white-space: nowrap; }
  @keyframes fx-pop { 0% { transform: translate(-50%,-50%) scale(0.2) rotate(-12deg); opacity: 0; }
    25% { transform: translate(-50%,-70%) scale(1.15) rotate(6deg); opacity: 1; }
    70% { transform: translate(-50%,-90%) scale(1) rotate(-3deg); opacity: 1; }
    100% { transform: translate(-50%,-130%) scale(0.9); opacity: 0; } }
  .fx-puff { position: fixed; pointer-events: none; z-index: 40; border-radius: 50%; background: rgba(255, 244, 220, 0.85);
    animation: fx-puff 0.7s ease-out forwards; }
  @keyframes fx-puff { from { transform: translate(-50%,-50%) scale(0.3); opacity: 0.9; }
    to { transform: translate(calc(-50% + var(--dx)), calc(-50% + var(--dy))) scale(1.4); opacity: 0; } }
  .fx-confetti { position: fixed; top: -5vh; width: 1.2vh; height: 2vh; z-index: 60; pointer-events: none; border-radius: 2px;
    animation: fx-fall var(--t) linear forwards; }
  @keyframes fx-fall { to { transform: translate(var(--dx), 115vh) rotate(var(--rot)); } }
  `;
  const st = document.createElement("style"); st.textContent = css + FROG_RIG_CSS; document.head.appendChild(st);
  const defs = document.createElement("div");
  defs.style.cssText = "position:absolute;width:0;height:0;overflow:hidden";
  defs.innerHTML = `<svg width="0" height="0"><defs>${[3, 11, 27].map((seed, i) =>
    `<filter id="ink${i}" x="-10%" y="-10%" width="120%" height="120%"><feTurbulence type="fractalNoise" baseFrequency="0.035" numOctaves="2" seed="${seed}"/>
     <feDisplacementMap in="SourceGraphic" scale="2.6" xChannelSelector="R" yChannelSelector="G"/></filter>`).join("")}</defs></svg>`;
  (document.body || document.documentElement).appendChild(defs);
})();

export function fxPop(x, y, text, size = 48, color) {
  const d = document.createElement("div");
  d.className = "fx-pop"; d.textContent = text;
  d.style.left = x + "px"; d.style.top = y + "px"; d.style.fontSize = size + "px";
  if (color) d.style.color = color;
  document.body.appendChild(d); setTimeout(() => d.remove(), 1200);
}
export function fxPuff(x, y, size = 30, n = 7) {
  for (let i = 0; i < n; i++) {
    const d = document.createElement("div"); d.className = "fx-puff";
    const a = Math.PI * (i / (n - 1)), r = size * (1.2 + Math.random());
    d.style.left = x + "px"; d.style.top = y + "px";
    d.style.width = d.style.height = size * (0.5 + Math.random() * 0.6) + "px";
    d.style.setProperty("--dx", Math.cos(a) * -r + "px"); d.style.setProperty("--dy", -Math.sin(a) * r * 0.5 + "px");
    document.body.appendChild(d); setTimeout(() => d.remove(), 750);
  }
}
export function fxConfetti(n = 120) {
  const cols = ["#f5a623", "#2ecc71", "#e84393", "#3498db", "#f1c40f", "#fff", "#9b59b6"];
  for (let i = 0; i < n; i++) {
    const d = document.createElement("div"); d.className = "fx-confetti";
    d.style.left = Math.random() * 100 + "vw"; d.style.background = cols[i % cols.length];
    d.style.setProperty("--t", 2 + Math.random() * 2.5 + "s");
    d.style.setProperty("--dx", (Math.random() - 0.5) * 30 + "vw");
    d.style.setProperty("--rot", (Math.random() - 0.5) * 1440 + "deg");
    d.style.animationDelay = Math.random() * 0.8 + "s";
    document.body.appendChild(d); setTimeout(() => d.remove(), 5500);
  }
}

export const NAME_BITS_A = ["Sir", "Captain", "Big", "Little", "Lady", "Doctor", "Sneaky", "Lucky", "Grumpy", "Fancy"];
export const NAME_BITS_B = ["Hopsworth", "Croakley", "Ribbiton", "Lilypad", "Puddles", "Wartface", "Toadstool", "Leapalot", "Bogsworth", "Splashy"];
export function randomFrogName() {
  const a = NAME_BITS_A[Math.floor(Math.random() * NAME_BITS_A.length)];
  const b = NAME_BITS_B[Math.floor(Math.random() * NAME_BITS_B.length)];
  return `${a} ${b}`;
}
