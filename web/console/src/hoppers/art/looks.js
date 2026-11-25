// More frog looks: the hats added after frog.js grew too long to hold them.
// hatSVG (frog.js) falls through to lookSVG for any hat it does not draw
// itself, and hands over its drawing helpers in k so these share the same
// ink-and-marker strokes. Same rules as frog.js: see the note at its top.

import { gravityLogoSVG } from './frog.js';

// k: st style string, S filled shape, L ink line, O outlined
// stroke, dot ink-ringed dot (see hatSVG).
export function lookSVG(hat, color, k) {
  const { st, S, L, O, dot } = k;
  switch (hat) {
    case "finance": {
      // orange knit beanie with a big fluffy brown bobble on top
      const pom = (cx, cy, r, n) => {
        let d = "";
        for (let i = 0; i < n; i++) {
          const a0 = (i / n) * Math.PI * 2, a1 = ((i + 1) / n) * Math.PI * 2, am = (a0 + a1) / 2, rr = r * (i % 2 ? 1.22 : 1.3);
          const p = (a, k) => `${(cx + k * Math.cos(a)).toFixed(2)} ${(cy + k * Math.sin(a)).toFixed(2)}`;
          d += (i ? " " : `M${p(a0, r)}`) + ` Q${p(am, rr)} ${p(a1, r)}`;
        }
        return d + " Z";
      };
      return S("M-14.5 -1 C-15.5 -11 -9 -17.5 0 -17.5 C9 -17.5 15.5 -11 14.5 -1 Z", "#f0801e", { band: "#c25a0c" }) +
        L("M-6 -16 C-8 -11 -8.5 -6 -8 -2 M1 -17.5 C0.5 -12 0.5 -6 0.8 -2 M8 -16 C9 -11 9.5 -6 9 -2", 1, "#9a4508") +
        S("M-16 -3.5 C-6 -5 6 -5 16 -3.5 L15.6 2.5 C6 1 -6 1 -15.6 2.5 Z", "#d9620f", { band: "#a84a08", w: 1.7 }) +
        L("M-12 -3.6 L-12 1.8 M-8 -4.2 L-8 1.2 M-4 -4.5 L-4 0.9 M0 -4.6 L0 0.8 M4 -4.5 L4 0.9 M8 -4.2 L8 1.2 M12 -3.6 L12 1.8", 0.9, "#7a3404") +
        S(pom(0.6, -24.6, 7.4, 14), "#8b5a2b", { band: "#5c3a1a", op: 0.92 }) +
        L("M-3.6 -27 l1.6 1.2 M1 -29.4 l0.4 1.8 M4.2 -26.6 l-1.4 1.2 M-1.4 -23.2 l1.6 0.6 M3.6 -22 l-0.6 -1.6 M-4.4 -22.4 l1.4 -1", 1, "#4a2c12") +
        L("M-3.4 -29.4 C-1.6 -30.8 0.8 -31 2.6 -30.4", 1.3, "#c9955f");
    }
    case "construction": {
      // Jamie's (Customer of the Year) hat: a New Era 9FIFTY flat-bill
      // snapback in "Flag Dark Green" -- tall structured front, dead-flat
      // bill, diamond-knit fabric all round, eyelets, a button on top, the
      // snap strap at the back, and the Gravity logo embroidered on the front.
      const G = "#1f4d3e", knit = [];
      for (let y = -15; y <= -2; y += 2.2) {
        for (let x = -11 + (Math.round(y * 10) % 44 === 0 ? 0 : 1.1); x <= 12.6; x += 2.2) {
          if (((x - 0.6) / 13.4) ** 2 + ((y + 0.4) / 16.8) ** 2 < 0.8 && !(x < -2 && y > -13 && y < -3.6)) knit.push(`M${x.toFixed(1)} ${y.toFixed(1)} h0.01`);
        }
      }
      return S("M-12.6 -0.4 C-13.8 -8 -11.8 -15.8 -2 -17 C8 -17.6 14.4 -10.6 13.8 -0.4 C5 1 -5 1 -12.6 -0.4 Z", G, { band: "#0f2a21", op: 0.94 }) +
        L(knit.join(" "), 0.8, "#33604f") +
        L("M-1.6 -17 C-3 -11 -3.2 -5 -2.8 0.6 M-1.6 -17 C5 -12.6 8 -6.4 8.6 0.2", 1, "#0b1f18") +
        L("M-11.4 -3 C-12.2 -9 -10.4 -14.6 -3.4 -15.8", 0.9, "#6fa38c") +
        dot(-0.4, -13.8, 0.55, "#0b1f18") + dot(5.6, -10.4, 0.55, "#0b1f18") +
        dot(-1.6, -17.3, 1.3, G) +
        // the snapback strap across the opening at the back
        S("M8.8 0.4 C9.4 -2.8 11.6 -3.8 13.7 -2.2 L13.8 -0.4 C12.2 0 10.6 0.3 8.8 0.4 Z", "#0b1f18", { band: false, op: 0.9, w: 1.2 }) +
        L("M9.4 -0.9 L13.6 -1.4", 1.1, "#173d30") +
        // flat bill, straight out the front
        S("M-11.8 -1.6 C-17 -1.9 -23 -1.5 -27.2 -0.8 L-26.8 2 C-20 2.2 -11 1.8 -4.8 1.2 C-7.2 0.2 -9.6 -0.8 -11.8 -1.6 Z", "#183f32", { band: "#0b221a", op: 0.94 }) +
        L("M-12.4 -0.2 C-17 -0.4 -22 -0.1 -25.8 0.4 M-12.8 0.8 C-17.4 0.7 -22 0.9 -25.6 1.3", 0.7, "#6fa38c") +
        `<g transform="translate(-7.4 -8.4) rotate(-6)">${gravityLogoSVG(4.6)}</g>`;
    }
    case "baron": {
      // property baron: a gold-rimmed monocle over the near eye on a little
      // gold chain. Drawn in head coordinates over the eyes (HAT_FIT 0,0).
      const ring = "M30.2 26.4 A7.4 7.4 0 1 0 45 26.4 A7.4 7.4 0 1 0 30.2 26.4 Z";
      const chain = "M43.6 31.2 C46.6 35 45.6 40 47.6 44.6 C48.6 47 50.4 48.4 52 49";
      return `<path d="${ring}" style="${st({ fill: "#dff3ff", "fill-opacity": 0.18, stroke: "none" })}"/>` +
        L(chain, 2.6) + `<path d="${chain}" style="${st({ fill: "none", stroke: "#f1c40f", "stroke-width": 1.3, "stroke-dasharray": "1.4 0.9", "stroke-linecap": "round" })}"/>` +
        O(ring, "#e0b020", 1.6) +
        L("M33.4 30 C32.4 27.4 33.2 24.4 35.6 22.6", 1, "#ffffffdd") +
        dot(52.2, 49.2, 1.1, "#f1c40f");
    }
    case "growler": {
      // Mr 2:58: a 64oz amber glass growler with a screw cap, a finger loop
      // at the neck and a Gravity label.
      return O("M2.8 -24.4 C8.4 -25.4 10 -20.6 7.2 -18", "#a2601a", 1.5) +
        S("M-8.6 0 C-3 1.4 3 1.4 8.6 0 C9.4 -5 9.6 -10 9.2 -14 C8.8 -17.4 5.6 -19.4 3.2 -21 L3 -25.6 L-3 -25.6 L-3.2 -21 C-5.6 -19.4 -8.8 -17.4 -9.2 -14 C-9.6 -10 -9.4 -5 -8.6 0 Z", "#a2601a", { band: "#5e3208", op: 0.88 }) +
        S("M-3.8 -25.2 L-3.6 -29.4 C-1.2 -30.2 1.2 -30.2 3.6 -29.4 L3.8 -25.2 C1.2 -24.6 -1.2 -24.6 -3.8 -25.2 Z", "#2d3436", { band: "#000", op: 0.9, w: 1.5, bw: 1.8 }) +
        L("M-1.8 -28.8 v3 M0 -29 v3 M1.8 -28.8 v3", 0.7, "#8a9496") +
        L("M-6.6 -14.6 C-7.4 -11 -7.4 -6 -6.8 -2.2 M-5.4 -17.4 C-4.2 -18.6 -3 -19.4 -2.2 -20", 1.4, "#ffe8c0") +
        S("M-6.4 -13 C-2 -12.2 2 -12.2 6.4 -13 L6.6 -3.6 C2 -2.8 -2 -2.8 -6.6 -3.6 Z", "#f2ede1", { band: false, op: 0.95, w: 1.3 }) +
        `<g transform="translate(0 -8.2)">${gravityLogoSVG(3.6)}</g>`;
    }
    case "garage": {
      // man cave: a big two-car garage with the roll-up door cracked open and
      // the light on inside (a beer fridge, some bottles), and a lit window up
      // in the gable.
      return S("M-20 0.5 L-20 -14 L0 -24 L20 -14 L20 0.5 C7 1.8 -7 1.8 -20 0.5 Z", "#8d99a6", { band: "#5f6b78" }) +
        L("M-19.6 -10.4 h4.2 M-19.6 -6.4 h4.2 M-19.6 -2.4 h4.2 M15.4 -10.4 h4.2 M15.4 -6.4 h4.2 M15.4 -2.4 h4.2 M-12.4 -17.4 h24.8", 0.8, "#5f6b78") +
        S("M-15.4 1.4 L-15.4 -12 L15.4 -12 L15.4 1.4 C5 2 -5 2 -15.4 1.4 Z", "#3b2a1c", { band: false, op: 0.95, w: 1.5 }) +
        `<path d="M-14.2 1.3 L-14.2 -3.6 L14.2 -3.6 L14.2 1.3 Z" style="${st({ fill: "#ffd76a", "fill-opacity": 0.9, stroke: "none" })}"/>` +
        S("M-11.6 1.2 L-11.6 -2.4 L-7.4 -2.4 L-7.4 1.2 Z", "#e74c3c", { band: false, op: 0.85, w: 1 }) +
        L("M3 -1.4 l0.6 2.2 M4.8 -1.6 l-0.4 2.4 M9.6 -2 v2.8", 1, "#7a4a24") +
        S("M-15.4 -12 L15.4 -12 L15.4 -3.8 L-15.4 -3.8 Z", "#f4f1e8", { band: "#b8b2a2", op: 0.95, w: 1.5, bw: 1.8 }) +
        L("M-15 -9.9 h30 M-15 -7.8 h30 M-15 -5.8 h30", 0.8, "#8e8878") + L("M-1.6 -4.8 h3.2", 1.2) +
        S("M-23.6 -13.2 L0 -27.4 L23.6 -13.2 L20.6 -11.6 L0 -23.6 L-20.6 -11.6 Z", "#4a4f57", { band: "#23262b", op: 0.92 }) +
        S("M-3 -20.6 L3 -20.6 L3 -16.2 L-3 -16.2 Z", "#ffd76a", { band: false, op: 0.95, w: 1.3 }) +
        L("M0 -20.6 v4.4 M-3 -18.4 h6", 0.8);
    }
    default:
      return "";
  }
}
