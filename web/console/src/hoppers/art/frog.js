// The frog art shared by the Hoppers TV and phone: an original cartoon frog
// in SVG, its hats, the heat-winner's rosette and the flies.
//
// THESE FILES ARE ES MODULES FOR THE PHONE AND FLAT SCRIPT FOR THE TV. The
// React phone imports them; the TV page, which has no build step, gets them
// inlined by Go (internal/apps/hoppers/render.go), which drops the import
// lines and the "export " keywords. So: only `export function`, `export
// const` and `export let` at the top level, imports only between these art
// files, and no `export default` or `export { ... }`. A test holds the line.

export const COLORS = [
  "#2ecc71", "#e84393", "#e67e22", "#3498db", "#9b59b6", "#f1c40f",
  "#1abc9c", "#e74c3c", "#8e5a2b", "#fd79a8", "#00cec9", "#636e72",
];

export const HATS = [
  "none", "crown", "cowboy", "beanie", "party", "viking", "propeller",
  "tophat", "flower", "pint", "chef", "headband", "bucket", "antlers",
  "silver", "curls", "cap", "blonde", "wild", "black", "ponytail", "pm",
];

export function shade(hex, amt) {
  const n = parseInt(hex.slice(1), 16);
  let r = (n >> 16) & 255, g = (n >> 8) & 255, b = n & 255;
  const t = amt < 0 ? 0 : 255, p = Math.abs(amt);
  r = Math.round((t - r) * p + r); g = Math.round((t - g) * p + g); b = Math.round((t - b) * p + b);
  return "#" + ((1 << 24) | (r << 16) | (g << 8) | b).toString(16).slice(1);
}

// Hats are drawn in their own local space: (0,0) is the middle of the hat's
// base (where it rests on the skull), y grows downward so the hat goes up into
// negative y. HAT_FIT then seats each one on the head bone (see frogSVG).
// Same "ink + marker" look as the frog: paper showing through a marker fill,
// a mis-registered marker band, wobbly ink outline. Styles go in style="" so
// page CSS (e.g. the phone's hat-thumbnail rules) can't restyle the strokes.
export function hatSVG(hat, color) {
  const I = "#1f1a17", P = "#f2ede1";
  const st = (o) => Object.entries(o).map(([k, v]) => `${k}:${v}`).join(";");
  // filled shape: paper + marker fill + offset marker band + ink outline
  const S = (d, c, o = {}) => `<path d="${d}" style="${st({ fill: P, stroke: "none" })}"/>` +
    `<path d="${d}" style="${st({ fill: c, "fill-opacity": o.op ?? 0.72, stroke: "none" })}"/>` +
    (o.band === false ? "" : `<path d="${d}" transform="translate(0.8 1)" style="${st({ fill: "none", stroke: o.band || c, "stroke-width": o.bw || 2.6, "stroke-linecap": "round", "stroke-linejoin": "round", opacity: 0.9 })}"/>`) +
    `<path d="${d}" style="${st({ fill: "none", stroke: I, "stroke-width": o.w || 1.9, "stroke-linecap": "round", "stroke-linejoin": "round" })}"/>`;
  // plain ink line
  const L = (d, w = 1.4, c = I) => `<path d="${d}" style="${st({ fill: "none", stroke: c, "stroke-width": w, "stroke-linecap": "round", "stroke-linejoin": "round" })}"/>`;
  // ink-outlined thick stroke (antlers, stalks)
  const O = (d, c, w) => L(d, w + 2.6) + L(d, w, c);
  const dot = (x, y, r, c) => `<circle cx="${x}" cy="${y}" r="${r}" style="${st({ fill: c, stroke: I, "stroke-width": 1.3 })}"/>`;
  switch (hat) {
    case "crown":
      return S("M-12 0.5 L-13.5 -11 L-6.5 -5 L-0.5 -14.5 L6 -5.5 L13.5 -11.5 L12 0.5 C5 2 -5 2 -12 0.5 Z", "#f7c600", { band: "#e0a800" }) +
        L("M-12.4 -3.2 C-5 -1.8 5 -1.8 12.4 -3.4", 1.2) +
        dot(-0.3, -6.6, 1.9, "#e74c3c") + dot(-7.5, -1.4, 1.1, "#3498db") + dot(7, -1.5, 1.1, "#2ecc71") +
        dot(-13.6, -11.6, 1.2, "#fff3c4") + dot(-0.5, -15.2, 1.2, "#fff3c4") + dot(13.6, -12.1, 1.2, "#fff3c4");
    case "cowboy":
      return S("M-10.5 -1 C-11.5 -8 -10 -15.5 -5.5 -16.5 C-2.5 -14.5 2.5 -14.5 5.5 -16.8 C10 -15.5 11.5 -8 10.5 -1 Z", "#b0753a") +
        S("M-24 -6.5 C-21 0 -11 2.2 0 2.2 C11 2.2 21 0 24.5 -7 C21 -3 15 -2.6 10 -2.6 C4 -1.2 -4 -1.2 -10 -2.6 C-15 -2.6 -21 -3 -24 -6.5 Z", "#8b5a2b") +
        S("M-10.7 -5.2 C-4 -3.8 4 -3.8 10.7 -5.2 L10.6 -2.6 C4 -1.2 -4 -1.2 -10.6 -2.6 Z", "#4a2c12", { band: false, w: 1.4 }) +
        L("M-1 -14.8 C-0.5 -11 -0.5 -8 -1 -6", 1.1);
    case "beanie":
      return S("M-14.5 -1 C-15.5 -11 -9 -17.5 0 -17.5 C9 -17.5 15.5 -11 14.5 -1 Z", "#d23b2a") +
        L("M-6 -16 C-8 -11 -8.5 -6 -8 -2 M1 -17.5 C0.5 -12 0.5 -6 0.8 -2 M8 -16 C9 -11 9.5 -6 9 -2", 1, "#7a1f14") +
        S("M-16 -3.5 C-6 -5 6 -5 16 -3.5 L15.6 2.5 C6 1 -6 1 -15.6 2.5 Z", "#a8291b", { w: 1.7 }) +
        L("M-12 -3.6 L-12 1.8 M-8 -4.2 L-8 1.2 M-4 -4.5 L-4 0.9 M0 -4.6 L0 0.8 M4 -4.5 L4 0.9 M8 -4.2 L8 1.2 M12 -3.6 L12 1.8", 0.9, "#5c150c") +
        S("M-3.5 -19 C-5 -22.5 -2 -25 0.5 -24 C3 -25.5 5.5 -22 3.8 -19.5 C2.5 -17 -2 -16.8 -3.5 -19 Z", "#ffffff", { band: "#ddd", op: 0.9 });
    case "party":
      return S("M-9 0 C-3 1.6 3 1.6 9 0 L1.5 -26 Z", "#6c5ce7") +
        L("M-6.9 -7.2 C-2 -5.8 3 -5.8 6.9 -7.4 M-4.6 -14.6 C-1.5 -13.6 2 -13.6 4.6 -14.8 M-2.5 -21 C-0.5 -20.4 1.5 -20.4 3 -21", 2.2, "#ffeaa7") +
        dot(-3, -10.5, 0.9, "#fd79a8") + dot(3.2, -3.6, 0.9, "#00cec9") + dot(1.6, -17.2, 0.8, "#fd79a8") +
        S("M-1 -26 C-3 -28.5 -1 -31.5 1.5 -30.5 C4 -31.5 5.5 -28 3.8 -26 C2.5 -24.5 0 -24.5 -1 -26 Z", "#fdcb6e", { band: "#f39c12" }) +
        L("M-8.6 0.4 C-10 4 -10.5 6 -10 8", 0.9);
    case "viking":
      return S("M-13.5 -6.5 C-18 -8 -21.5 -13 -20.5 -21 C-18 -15 -15 -13 -11 -12.5 Z", "#f5f0e1", { band: "#d8cfb4" }) +
        S("M13.5 -6.5 C18 -8 21.5 -13 20.5 -21 C18 -15 15 -13 11 -12.5 Z", "#f5f0e1", { band: "#d8cfb4" }) +
        S("M-14 -1 C-14.5 -10 -8 -15.5 0 -15.5 C8 -15.5 14.5 -10 14 -1 Z", "#9aa6a8", { band: "#6d7a7c" }) +
        S("M-15.5 -3.5 C-6 -5 6 -5 15.5 -3.5 L15.3 1.2 C6 -0.2 -6 -0.2 -15.3 1.2 Z", "#c9a227", { band: "#9c7b12", w: 1.6 }) +
        L("M0 -15.3 L0 -4.5", 1.1) + dot(-10, -1.4, 0.8, "#fff3c4") + dot(-3.5, -2, 0.8, "#fff3c4") + dot(3.5, -2, 0.8, "#fff3c4") + dot(10, -1.4, 0.8, "#fff3c4");
    case "propeller":
      return S("M-13.5 -0.5 C-14 -9 -8 -14 0 -14 C8 -14 14 -9 13.5 -0.5 Z", "#0984e3") +
        S("M-13.5 -0.5 C-14 -9 -8 -14 0 -14 C-2 -10 -2.5 -5 -2 -0.5 Z", "#fdcb6e", { band: "#f39c12" }) +
        S("M-12 -0.8 C-17 -1 -21 0.5 -23 2.5 C-17 3.6 -10 2.6 -6 1 Z", "#d63031") +
        O("M0 -14 L0 -18.5", "#666", 1.4) +
        S("M0 -19 C-4 -21.5 -10 -21.8 -12.5 -20 C-10 -18.2 -4 -18 0 -19 Z", "#d63031") +
        S("M0 -19 C4 -16.5 10 -16.3 12.5 -18 C10 -20 4 -20.2 0 -19 Z", "#00b894") + dot(0, -19, 1.3, "#fdcb6e");
    case "tophat":
      return S("M-9 -2 L-10.2 -24 C-4 -25.6 4 -25.6 10.2 -24.2 L9 -2 Z", "#2d3436", { band: "#000", op: 0.92 }) +
        L("M-10.2 -24 C-4 -22.4 4 -22.4 10.2 -24.2", 1.3) +
        S("M-9.4 -8 C-3 -6.6 3 -6.6 9.4 -8 L9.2 -3.6 C3 -2.4 -3 -2.4 -9.2 -3.6 Z", "#d63031", { band: false, w: 1.4 }) +
        S("M-16.5 -3.5 C-13 1.8 13 1.8 16.5 -3.8 C13 -1.6 9 -1.4 0 -1.6 C-9 -1.4 -13 -1.6 -16.5 -3.5 Z", "#2d3436", { band: "#000", op: 0.92 }) +
        L("M-6.5 -21 L-6.8 -11", 1, "#8a9496");
    case "flower": {
      const petals = [0, 60, 120, 180, 240, 300].map(a => `<g transform="rotate(${a})">${S("M0 -1.5 C-3.6 -4 -3.6 -10 0 -11 C3.6 -10 3.6 -4 0 -1.5 Z", "#ffffff", { band: "#e8e2d0", op: 0.95, w: 1.5, bw: 1.8 })}</g>`).join("");
      return O("M-2 2 C-4 -2 -4 -6 -2 -9", "#27ae60", 1.4) +
        S("M-3.6 -3 C-8.5 -5 -10 -2 -9.5 0 C-7 0.5 -4.5 -0.5 -3.6 -3 Z", "#2ecc71", { band: "#1e8449", w: 1.4, bw: 1.8 }) +
        `<g transform="translate(-1 -12) rotate(14)">${petals}${dot(0, 0, 3.5, "#f6b93b")}</g>`;
    }
    case "pint":
      return S("M-7.5 0 C-2.5 1.2 2.5 1.2 7.5 0 L9.8 -19 L-9.8 -19 Z", "#f39c12", { band: "#d35400", op: 0.82 }) +
        L("M-6.5 -14 C-6 -9 -5.6 -5 -5.2 -2", 1.6, "#fff7e6") +
        dot(2, -6, 0.7, "#fff3c4") + dot(4.5, -11, 0.6, "#fff3c4") + dot(1, -14, 0.8, "#fff3c4") +
        S("M-10.6 -18 C-13 -20 -11.5 -24 -8 -23.5 C-6.5 -27 -1.5 -27 0 -24.5 C2 -27.5 7 -26.5 7.5 -23.5 C11 -24.5 13 -20.5 10.6 -18 C4 -16.6 -4 -16.6 -10.6 -18 Z", "#fffbe6", { band: "#e8dcb0", op: 0.95 }) +
        L("M8.6 -17.4 C9.6 -14.5 9.3 -12 8.9 -10.5", 1.3, "#fffbe6");
    case "chef":
      return S("M-12 -14 C-17 -16 -17 -25 -10.5 -25.5 C-9 -31.5 -1 -32.5 1.5 -28 C5 -32 13 -30 12.5 -24 C18 -22.5 17 -15 12 -14 Z", "#ffffff", { band: "#ddd8c8", op: 0.95 }) +
        L("M-4.5 -14.6 C-5.5 -18 -5 -21 -3.5 -23.5 M4.5 -14.6 C5.5 -18 5.2 -21 3.6 -23.5", 1.1) +
        S("M-11.5 -1 L-12 -14 C-4 -12.6 4 -12.6 12 -14 L11.5 -1 C4 0.4 -4 0.4 -11.5 -1 Z", "#ffffff", { band: "#ddd8c8", op: 0.95 }) +
        L("M-7.5 -12.8 L-7.4 -0.6 M-2.5 -12.4 L-2.5 -0.2 M2.5 -12.4 L2.5 -0.2 M7.5 -12.8 L7.4 -0.6", 0.9, "#a49c88");
    case "headband":
      // rides round the skull behind the eye bulges; tails flutter off the back
      return S("M-21 -2 C-12 -6.5 12 -6.5 21 -2.5 L21.4 3 C12 -1.2 -12 -1.2 -21.4 3.5 Z", "#e84393", { band: "#b8256f", w: 1.8 }) +
        L("M-14 -2.6 C-5 -5 5 -5 14 -2.8", 1, "#fff3c4") +
        S("M20.5 -1 C25 -3.5 28 -2 31 -4.5 C30.5 -1.5 27.5 0.5 22 1.2 Z", "#e84393", { band: "#b8256f", w: 1.6, bw: 2 }) +
        S("M20.5 1 C25 1.5 27.5 4.5 31 4 C29 6.5 25 6.5 21.3 3.8 Z", "#e84393", { band: "#b8256f", w: 1.6, bw: 2 });
    case "bucket":
      return S("M-11 -3 C-12 -10 -8.5 -14.5 0 -14.5 C8.5 -14.5 12 -10 11 -3 Z", "#c8b88a", { band: "#9e8c57" }) +
        S("M-11.5 -4.8 C-4 -3.4 4 -3.4 11.5 -4.8 L11.2 -1.8 C4 -0.6 -4 -0.6 -11.2 -1.8 Z", "#6b5a2e", { band: false, w: 1.4 }) +
        S("M-12 -2.5 C-16 -1.5 -19.5 1.5 -21 4.5 C-12 7 12 7 21 4.5 C19.5 1.5 16 -1.5 12 -2.5 C4 -1 -4 -1 -12 -2.5 Z", "#c8b88a", { band: "#9e8c57" }) +
        L("M-17.5 2.6 C-8 4.8 8 4.8 17.5 2.6", 0.9, "#8e7f55") + L("M-5 -13.6 C-6 -10 -6 -7 -5.6 -5.2", 0.9, "#8e7f55");
    case "antlers":
      return O("M-6 0 C-8 -6 -9 -10 -13 -14 C-16 -17 -17 -21 -16 -25 M-13 -14 C-17 -14 -21 -16 -23 -19 M-9.4 -8.5 C-6 -12 -5.6 -16 -6.6 -19", "#a0672f", 2.6) +
        O("M6 0 C8 -6 9 -10 13 -14 C16 -17 17 -21 16 -25 M13 -14 C17 -14 21 -16 23 -19 M9.4 -8.5 C6 -12 5.6 -16 6.6 -19", "#a0672f", 2.6) +
        L("M-14 -16 C-15 -19 -15.4 -21 -15 -23 M14 -16 C15 -19 15.4 -21 15 -23", 0.9, "#d4a373");
    case "silver": {
      // neat short silver hair with a side part. Drawn behind the eyes (see
      // HAT_FIT), so it can come right down to the lids and the eyes sit on it.
      // The part is a stripe of the frog's own skin between the two combed sections.
      const back = "M-5 -11.6 C3 -12.6 11 -11.6 16 -8.2 C20.5 -5 22.2 -0.8 21.6 4 L19.6 2.4 L18.8 5.4 L16.6 2.6 C14.5 1.2 12 0.8 9 0.8 C3 0.6 -3 0.4 -8 0.8 L-6.5 -6 Z";
      const front = "M-6.4 -12.6 C-12.5 -13.4 -18.8 -10.4 -20.6 -4.6 C-21.4 -2 -20.8 0.6 -19.4 2.4 C-18.4 -0.8 -16 -3.4 -12.6 -4.6 C-9.5 -5.8 -6.6 -7.6 -5.4 -10.4 Z";
      const part = "M-5.6 -11.8 C-5 -9.6 -4.6 -7.4 -4.6 -5";
      return S(back, "#dde1e5", { band: "#8a939b", op: 0.95, w: 1.7 }) +
        L("M-2 -9 C4 -10 10.5 -8.8 15.5 -5.6 M-2.6 -5.6 C4 -6.6 11 -5 16.8 -1.2 M-1.6 -2 C5 -2.6 11 -1.2 15.2 1.8", 1.2, "#5d666e") +
        L(part, 2.8, P) + L(part, 2.8, color + "8c") + L(part, 1.1) +
        S(front, "#eef0f2", { band: "#9aa2a9", op: 0.95, w: 1.7 }) +
        L("M-8.2 -11.4 C-12.8 -11 -16.6 -8.4 -18.4 -4.2 M-7.8 -8.4 C-11.4 -7.8 -14.6 -5.8 -16.6 -2.6", 1.2, "#6b747c") +
        L("M2 -10.8 C6.5 -10.8 10.5 -9.6 13.4 -7.6 M-15.4 -10 C-12.8 -11.6 -10.6 -12 -8.6 -12", 1.4, "#ffffff");
    }
    case "curls": {
      // a mop of light-brown curls: overlapping ringlets, each with a spiral.
      // Back ones first, front ones overlap them. Also behind the eyes.
      const curl = ([x, y, r]) => S(`M${x - r} ${y} a${r} ${r} 0 1 0 ${2 * r} 0 a${r} ${r} 0 1 0 ${-2 * r} 0 Z`, "#c98b4a", { band: "#8f5a26", op: 0.85, w: 1.6, bw: 2 }) +
        L(`M${x - r * 0.45} ${y + r * 0.1} A${r * 0.4} ${r * 0.4} 0 1 1 ${x + r * 0.25} ${y + r * 0.45} A${r * 0.6} ${r * 0.6} 0 0 1 ${x - r * 0.15} ${y - r * 0.6}`, 1, "#6b3f17");
      return [[20, 9, 4], [19, 2, 4.5], [-18, 3, 4.2], [15, -4, 5], [-14.5, -3.5, 4.8], [8, -8.5, 5.2], [-7.5, -8.5, 5.2], [0.3, -10, 5.3],
        [11, 0, 4.4], [-10, 0.5, 4.2], [-1, -3, 4.6], [5.5, -2.5, 4.4]].map(curl).join("");
    }
    case "cap": {
      // baseball cap, brim forward (toward the way the frog faces = -x).
      // The front panel carries the Gravity Brewing badge.
      const N = "#24365e";
      return S("M-12.5 -0.5 C-13.5 -9 -7.5 -15 1 -15 C9.5 -15 14.5 -9 13.8 -0.5 C5 1 -5 1 -12.5 -0.5 Z", N, { band: "#121d36", op: 0.88 }) +
        L("M1 -15 C-0.5 -10 -0.8 -5 -0.4 0.3 M1 -15 C6 -10.5 8 -5.5 8.4 -0.3", 1, "#0d1528") +
        dot(1, -15.2, 1.3, N) +
        S("M-11.5 -1.2 C-16.5 -1.8 -22.5 -0.5 -26 2.2 C-21 4.6 -12 3.8 -5 1.2 C-7 0.4 -9.5 -0.6 -11.5 -1.2 Z", "#1a2847", { band: "#0d1528", op: 0.9 }) +
        L("M-12 0.8 C-16.5 0.6 -21 1.4 -24 2.6", 0.9, "#6c7da3") +
        `<g transform="translate(-6.4 -6.8) rotate(-6)"><circle r="4.4" style="fill:#f2ede1;stroke:#1f1a17;stroke-width:1.1"/>${gravityLogoSVG(3.9)}</g>`;
    }
    case "blonde": {
      // long straight blonde hair: a side-swept fringe up front and a straight
      // curtain down the back of the head, blunt-cut. Behind the eyes.
      const back = "M-5 -12.8 C6 -13.4 16 -10.4 20.4 -4.6 C23 -1 23.6 7 23.8 15 L24.4 25 C20 26.2 15.5 26.2 11.4 25 L11.8 14 C12 8 11 4 8 1.4 C2 0.3 -4 0.1 -8.5 0.6 L-6.5 -6 Z";
      const front = "M-5.6 -12.8 C-12.8 -13 -19 -9.4 -21 -3.4 C-21.8 -0.6 -21.4 2.6 -20.4 5 C-19 1 -16.4 -2.2 -12.8 -3.6 C-9.6 -4.8 -6.8 -7.6 -5.6 -12.8 Z";
      return S(back, "#f6d365", { band: "#d4a017", op: 0.95, w: 1.7 }) +
        L("M-2 -9.6 C5 -10.6 12 -9 16.6 -5 M14.6 1 L14.3 24.6 M18 -0.6 L18.2 25 M21.3 2 L21.5 24.6", 1.1, "#a87b12") +
        S(front, "#fbe08a", { band: "#d4a017", op: 0.95, w: 1.7 }) +
        L("M-8.4 -11.4 C-13 -10.8 -16.8 -8 -18.6 -3.8 M-8 -8.4 C-11.6 -7.6 -14.8 -5.6 -16.8 -2.4", 1.1, "#a87b12") +
        L("M2 -11.2 C7 -11.2 11.5 -9.8 14.6 -7.4 M21.6 4 L21.8 12", 1.4, "#ffffff");
    }
    case "wild": {
      // wild, longish, mostly grey hair: spiky tufts everywhere, longer at the back.
      const mop = "M-19.5 3.5 L-23.5 -0.5 L-19.6 -3.6 L-23.2 -8.8 L-16.6 -9.6 L-17.2 -15.6 L-10.6 -12.6 L-7.2 -18.6 L-2.6 -13.6 L2.6 -19.2 L5 -13.2 L11.6 -17.2 L11.6 -11 L18.6 -12.6 L16.6 -6.6 L24 -6 L20 -1.2 L26 2.6 L21 4.6 L25.6 10 L19.6 9.6 L21.6 16.2 L15.6 12 L13 16.6 L11.6 8 C10 4 7 1.6 3 1 C-2 0.4 -6 0.4 -9 0.8 L-12.2 -1 L-15 3.6 Z";
      return S(mop, "#c3c7cb", { band: "#7d848a", op: 0.92, w: 1.7 }) +
        L("M-15 -8 C-9 -11 -3 -11.4 2 -10.2 M4 -9 C10 -9 15 -6 18.4 -1 M12.6 2 C15.6 5 17.4 9 18.2 12.6 M-9 -5 C-4.6 -7 1.4 -7.2 6 -5.2 M-18 -2 C-16 -4.6 -13.4 -6 -10.6 -6.6", 1.1, "#4f565c") +
        // a few dark streaks left over from before the grey took over
        L("M-12 -10.4 C-8 -12.6 -4 -13 -0.6 -12.4 M8 -6 C12 -5 15 -2.6 16.6 0.6 M14 7 C15.4 9 16 11 16.2 13", 1.6, "#5a4636") +
        L("M-4 -14.6 C-1 -15.6 2 -15.4 4.4 -14.4 M14 -9.6 C16 -8.4 17.6 -6.8 18.6 -5", 1.3, "#ffffff");
    }
    case "black": {
      // short, tidy black hair cropped close to the skull. Behind the eyes.
      const crop = "M-19.6 2 C-21 -4 -17 -11.6 -6 -12.8 C6 -13.6 16 -10.6 20.2 -4.6 C22 -2 22.4 1.6 21.8 4.6 L19.4 3 C17 1.2 13 0.6 9 0.6 C3 0.4 -3 0.3 -8 0.6 C-12 -0.6 -15.6 0.2 -17.8 3.4 Z";
      return S(crop, "#2b2b2e", { band: "#000000", op: 0.92, w: 1.7 }) +
        L("M-15 -8 l1.6 -1.8 M-10 -10.6 l1.6 -1.8 M-4 -11.8 l1.4 -1.8 M3 -12 l1.4 -1.6 M9 -10.8 l1.4 -1.4 M14.6 -8 l1.3 -1.2 M-12 -5 l1.4 -1.6 M-5 -7.6 l1.4 -1.6 M2 -8 l1.4 -1.5 M9 -6.6 l1.3 -1.3 M15 -3.6 l1.2 -1.2", 0.9, "#6b7280") +
        L("M-6 -10.6 C0 -11.6 6 -11 11 -9.2", 1.3, "#9aa4ad");
    }
    case "ponytail": {
      // straight brown hair pulled back tight into a ponytail: a smooth cap
      // combed back to a hair tie at the back of the head, and the tail
      // swinging out behind and down. Behind the eyes.
      const cap = "M-19.8 2.4 C-21.2 -4 -17 -11.8 -6 -13 C6 -13.8 15.6 -11 19.6 -6 C21.4 -3.6 21.8 -0.4 21.2 2.6 L18.8 1.4 C16 0.6 12.6 0.4 9 0.5 C3 0.3 -3 0.2 -8 0.6 C-12 -0.4 -15.8 0.4 -18 3.6 Z";
      const tail = "M18.4 -9.6 C26 -12.4 33.6 -8.6 35.4 -1.4 C37 5 35.8 12.6 32.6 20 C31 14.6 29 9.6 26.2 5.6 C23.6 2 21 -1.4 18 -4 Z";
      return S(tail, "#7a4a24", { band: "#4e2e14", op: 0.95, w: 1.7 }) +
        L("M22 -9 C28.6 -8.6 32.6 -3.6 33 2.6 C33.2 8 32.4 12.6 31.2 16.6 M21 -5.6 C25 -3.6 28.4 0.6 29.6 6.2", 1.1, "#4e2e14") +
        S(cap, "#8a5529", { band: "#4e2e14", op: 0.95, w: 1.7 }) +
        // combed straight back toward the tie
        L("M-16.6 -5.6 C-8 -10 6 -10.6 17.6 -7.4 M-15.4 -1.6 C-6 -6 6 -6.6 17.8 -5.2 M-10 0 C-2 -2.6 8 -2.6 18 -3.4", 1.1, "#4e2e14") +
        L("M-8 -11.4 C0 -12.4 8 -11.6 14 -9.4", 1.4, "#c08a52") +
        // the hair tie
        S("M16.8 -10.4 C18.6 -11.4 20.8 -10.8 21.4 -9 L20.4 -3.4 C19.4 -2.4 17.6 -2.6 16.8 -3.8 Z", "#e74c3c", { band: "#a93226", op: 0.95, w: 1.4, bw: 1.6 });
    }
    case "pm": {
      // bald with glasses: the frog's own bald head, plus rectangular specs
      // across both eyes with an arm back to the ear. Drawn in head
      // coordinates over the eyes (HAT_FIT puts it at 0,0).
      const lens = (x, y, w, h) => `<rect x="${x}" y="${y}" width="${w}" height="${h}" rx="2.2" style="fill:#dff3ff;fill-opacity:.15;stroke:#1f1a17;stroke-width:1.9"/>` +
        L(`M${x + 1.6} ${y + h - 2} L${x + w * 0.45} ${y + 1.6}`, 0.9, "#ffffffcc");
      return lens(31, 21.2, 14.2, 9.6) + lens(51.6, 21.6, 12.4, 8.8) +
        L("M45.2 25.2 C47.4 23.8 49.6 23.8 51.6 25.4", 1.7, "#1f1a17") +
        L("M64 24.6 C66.4 24.4 68.6 25 70.4 26.4", 1.7, "#1f1a17");
    }
    default:
      return "";
  }
}

// Gravity Brewing "hop drop" mark (from docs/reference/gravity-logo.jpg, the
// part above the wordmark): an orange-to-yellow U swoosh with a green hop cone
// sitting in its bottom. Clean vector, not ink-wobbly, since it's a logo.
// Drawn centred on (0,0) to fit a circle of radius r (viewBox units).
// To swap in an official file later, replace the body with e.g.
//   `<image href="/static/gravity-hopdrop.svg" x="${-r}" y="${-r}" width="${2 * r}" height="${2 * r}"/>`
// (square, transparent background, SVG preferred, cropped to just the hop drop).
let gravityLogoN = 0;
export function gravityLogoSVG(r = 4) {
  const id = "gvd" + (++gravityLogoN), st = (f) => `style="fill:${f};stroke:none"`;
  const scale = (x, y, w, h) => `<path d="M${x - w} ${y - h * 0.35} Q${x - w * 0.9} ${y + h * 0.55} ${x} ${y + h * 0.75} Q${x + w * 0.9} ${y + h * 0.55} ${x + w} ${y - h * 0.35} Q${x} ${y + h * 0.05} ${x - w} ${y - h * 0.35} Z" ${st("#7cc142")}/>`;
  return `<g transform="scale(${r / 10})">
    <defs><linearGradient id="${id}" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#e2611f"/><stop offset="0.55" stop-color="#f39a1e"/><stop offset="1" stop-color="#f7cf1f"/></linearGradient></defs>
    <path d="M-6.6 -10 C-6.6 1.5 -4.4 9.2 0 9.2 C4.4 9.2 6.6 1.5 6.6 -10 L6.1 -10 C5.6 0.5 3.4 5.9 0 5.9 C-3.4 5.9 -5.6 0.5 -6.1 -10 Z" style="fill:url(#${id});stroke:none"/>
    <path d="M0 -7.2 C2.6 -5.2 4.1 -2.2 4 0.9 C3.9 3.6 2.2 5.4 0 5.4 C-2.2 5.4 -3.9 3.6 -4 0.9 C-4.1 -2.2 -2.6 -5.2 0 -7.2 Z" ${st("#2e7d32")}/>
    <path d="M-0.5 -7.4 C-0.4 -8.6 0.4 -9.2 1.2 -9.3" style="fill:none;stroke:#2e7d32;stroke-width:0.7;stroke-linecap:round"/>
    ${scale(0, -4.6, 1.5, 2.2)}${scale(-1.8, -1.6, 1.5, 2.4)}${scale(1.8, -1.6, 1.5, 2.4)}${scale(-2.4, 1.4, 1.2, 2.2)}${scale(2.4, 1.4, 1.2, 2.2)}
    <path d="M0 -0.6 C1.4 1 2 2.3 2 3.2 C2 4.4 1.1 5.1 0 5.1 C-1.1 5.1 -2 4.4 -2 3.2 C-2 2.3 -1.4 1 0 -0.6 Z" ${st("#b5dc7a")}/>
  </g>`;
}

// Where each hat sits on the head bone: [x, y, rotate°, scale, under-eyes?].
// (x,y) is the hat base's middle in frog viewBox units. The skull top is the
// two eye bulges (peaks ~(36,19) near / ~(58,20) far, dip ~(48,22)); hats
// rest across them with a slight backward tilt (the frog faces left).
// "under" hats are drawn beneath the eyeballs so the eyes poke out over them.
export const HAT_FIT = {
  crown: [47.5, 20.5, 6, 1], cowboy: [47.5, 21, 5, 1], beanie: [47.5, 21.6, 5, 1],
  party: [46, 20, 12, 1], viking: [47.5, 21, 4, 1], propeller: [47.5, 20.5, 4, 1],
  tophat: [47.5, 21.4, 7, 0.95], flower: [60, 21, 16, 0.9], pint: [47.8, 22, 3, 0.95],
  chef: [47.5, 21.2, 5, 1], headband: [46, 24.5, 3, 1, true], bucket: [47.5, 21.2, 4, 1],
  antlers: [47.5, 21.5, 3, 1, true],
  silver: [47.5, 21.5, 2, 1, true], curls: [47.5, 21.5, 2, 1, true], cap: [47.5, 20.8, 4, 1],
  blonde: [47.5, 21.5, 2, 1, true], wild: [47.5, 21.5, 2, 1, true], black: [47.5, 21.5, 2, 1, true],
  ponytail: [47.5, 21.5, 2, 1, true], pm: [0, 0, 0, 1],
};
export function placedHat(hat, color, layer) {
  const f = HAT_FIT[hat]; if (!f || !!f[4] !== (layer === "under")) return "";
  return `<g transform="translate(${f[0]} ${f[1]}) rotate(${f[2]}) scale(${f[3]})"><g class="hat">${hatSVG(hat, color)}</g></g>`;
}

// Hand-drawn "ink + marker" frog: wobbly ink outline, a marker band of the
// frog's colour hugging (and slightly missing) the line, paper showing inside,
// sleepy heavy-lidded eyes, warts, red-tipped toes. opts.mood: "wow".
export const INK = "#1f1a17", PAPER = "#f2ede1", WART = "#8a5a2e", TOE = "#c0392b";
// Rigged frog. Each bone is a <g class="rig-*"> whose pivot (in viewBox units,
// viewBox starts at 0,0 so transform-box:view-box origins are plain coords) is
// set in FROG_RIG_CSS below. Animations target the bones by class.
//   root   (54,93) whole frog, squash & stretch
//   body   torso, warts, belly marks
//   throat (34,45) croak sac
//   head   (45,42) eyes, lids, brows, mouth, hat
//   armF   (54,60) far arm + fingers
//   armN   (40,51) near arm  > handN (27,85) fingers
//   thigh  (68,72) back thigh > foot (60,89) foot + toes
export function frogSVG(color, hat, opts = {}) {
  const blink = (Math.random() * 5).toFixed(2), breathe = (Math.random() * 2).toFixed(2);
  const wow = opts.mood === "wow";
  const headFill = "M24 38 C22 32 26 27 31 26 C31 19 41 16 44 22 C47 20 50 20 52 23 C54 18 63 19 63 26 C68 29 70 35 68 41 C58 47 38 47 24 38 Z";
  const headLine = "M24 38 C22 32 26 27 31 26 C31 19 41 16 44 22 C47 20 50 20 52 23 C54 18 63 19 63 26 C68 29 70 35 68 41";
  const bodyFill = "M26 40 C30 48 29 57 29 66 C28 80 31 91 40 92 L68 92 C76 93 82 89 83 81 C85 71 83 59 79 47 C76 37 71 30 63 26 C55 34 38 40 26 40 Z";
  const bodyLine = "M27 41 C30 49 29 57 29 66 C28 80 31 91 40 92 L68 92 C76 93 82 89 83 81 C85 71 83 59 79 47 C76 37 71 30 63 26";
  const thigh = "M58 72 C66 63 81 67 80 80 C79 88 71 91 62 89 C56 88 54 80 58 72 Z";
  const foot = "M61 89 C55 91 50 91 46 91";
  const armNear = "M37 48 C29 57 30 70 24 84 L30 86 C35 72 36 62 44 56 Z";
  const armFar = "M51 58 C47 68 49 79 44 87 L49 88 C54 79 53 69 57 62 Z";
  const band = (d, w = 6.5) => `<path d="${d}" fill="none" stroke="${color}" stroke-width="${w}" stroke-linecap="round" stroke-linejoin="round" transform="translate(1.8 2)"/>`;
  const ink = (d, w = 2.3) => `<path d="${d}" fill="none" stroke="${INK}" stroke-width="${w}" stroke-linecap="round" stroke-linejoin="round"/>`;
  const fill = d => `<path d="${d}" fill="${PAPER}"/><path d="${d}" fill="${color}" opacity="0.55"/>`;
  const shape = (d, w) => `${fill(d)}${band(d, w)}${ink(d)}`;
  const toes = (pts, ox, oy) => pts.map(([x, y]) =>
    `<path d="M${ox} ${oy} Q${(ox + x) / 2} ${(oy + y) / 2 + 1.5} ${x} ${y}" stroke="${INK}" stroke-width="1.9" fill="none" stroke-linecap="round"/>
     <ellipse cx="${x}" cy="${y}" rx="2.1" ry="1.6" fill="${TOE}" stroke="${INK}" stroke-width="1"/>`).join("");
  const eye = (cx, cy, rx, ry) => `
    <ellipse cx="${cx}" cy="${cy}" rx="${rx}" ry="${ry}" fill="#fff" stroke="${INK}" stroke-width="1.9"/>
    <g class="pupils"><ellipse cx="${cx - 0.8}" cy="${wow ? cy : cy + 1.4}" rx="${wow ? rx * 0.48 : rx * 0.42}" ry="${wow ? ry * 0.62 : ry * 0.45}" fill="${INK}"/>
      <circle cx="${cx}" cy="${wow ? cy - 1 : cy + 0.9}" r="0.8" fill="#fff"/></g>
    ${wow ? "" : `<g class="lid"><path d="M${cx - rx} ${cy} A${rx} ${ry} 0 0 1 ${cx + rx} ${cy} Z" fill="${PAPER}"/>
      <path d="M${cx - rx} ${cy} A${rx} ${ry} 0 0 1 ${cx + rx} ${cy} Z" fill="${color}" opacity="0.5"/>
      <path d="M${cx - rx - 0.5} ${cy + 0.3} Q${cx} ${cy - 1.2} ${cx + rx + 0.5} ${cy + 0.3}" stroke="${INK}" stroke-width="2.1" fill="none" stroke-linecap="round"/></g>`}`;
  const mouth = wow
    ? `<ellipse cx="33" cy="40" rx="4.5" ry="3.6" fill="#5a1e1e" stroke="${INK}" stroke-width="1.8"/>`
    : ink("M24.5 38.5 C31 41.5 39 41.5 47 39.5", 2.1);
  return `<svg class="frogsvg" viewBox="0 0 100 100" xmlns="http://www.w3.org/2000/svg" style="--blink:${blink}s;--breathe:${breathe}s">
    <ellipse class="shadow" cx="54" cy="94" rx="36" ry="4" fill="rgba(0,0,0,0.28)"/>
    <g class="rig-root">
      <g class="rig-thigh">
        ${shape(thigh, 5.5)}
        <g class="rig-foot">${band(foot, 4.5)}${ink(foot)}${toes([[53, 94], [57, 95]], 58, 90)}</g>
      </g>
      <g class="rig-body">
        ${fill(bodyFill)}${band(bodyLine)}${ink(bodyLine, 2.4)}
        <path d="M33 70 l2.2 -1.6 M36 77 l2.2 -1.6 M44 66 l2 -1.4" stroke="${INK}" stroke-width="1.1" stroke-linecap="round"/>
        <ellipse cx="72" cy="40" rx="1.7" ry="3.4" fill="${WART}" transform="rotate(-30 72 40)"/>
        <ellipse cx="78" cy="55" rx="1.8" ry="3.6" fill="${WART}" transform="rotate(-15 78 55)"/>
        <ellipse cx="81" cy="68" rx="1.6" ry="3" fill="${WART}"/>
        <circle cx="75" cy="47" r="1.1" fill="${WART}"/>
      </g>
      <g class="rig-thigh-front">${shape(thigh, 5.5)}<ellipse cx="70" cy="76" rx="1.3" ry="2.2" fill="${WART}" transform="rotate(20 70 76)"/></g>
      <g class="rig-armF">${shape(armFar, 4)}${toes([[40, 92], [43, 95], [48, 95.5], [52, 93]], 46.5, 88)}</g>
      <g class="rig-throat"><ellipse cx="35" cy="44" rx="7" ry="4" fill="${PAPER}"/><ellipse cx="35" cy="44" rx="7" ry="4" fill="${color}" opacity="0.35"/>
        <path d="M28.5 45 C31 48.5 39 48.5 41.5 45" stroke="${INK}" stroke-width="1.4" fill="none" stroke-linecap="round"/></g>
      <g class="rig-head">
        ${fill(headFill)}${band(headLine)}${ink(headLine, 2.4)}
        <path d="M33 21 Q38 17 44 19.5" stroke="${color}" stroke-width="3.2" fill="none" stroke-linecap="round" transform="translate(1 2.2)"/>
        <path d="M52 21.5 Q57 17.5 62 20.5" stroke="${color}" stroke-width="3" fill="none" stroke-linecap="round" transform="translate(1 2.2)"/>
        <ellipse cx="29" cy="31.5" rx="1.1" ry="0.8" fill="${INK}"/>
        ${mouth}
        ${placedHat(hat, color, "under")}
        ${eye(38, 26, 6.2, 4.8)}${eye(57.5, 26, 5.2, 4.2)}
        ${placedHat(hat, color, "over")}
      </g>
      <g class="rig-armN">${shape(armNear, 4.5)}
        <g class="rig-handN">${toes([[17, 88], [19.5, 92.5], [24.5, 94.5], [30, 93]], 27, 86)}</g>
      </g>
      ${opts.badge ? rosetteSVG(color) : ""}
    </g>
  </svg>`;
}

export const FROG_RIG_CSS = `
  .frogsvg [class^="rig-"] { transform-box: view-box; }
  .frogsvg .rig-root { transform-origin: 54px 93px; }
  .frogsvg .rig-head { transform-origin: 45px 42px; animation: rig-head-idle 5s ease-in-out infinite; animation-delay: calc(var(--breathe) * -2); }
  .frogsvg .rig-throat { transform-origin: 35px 42px; animation: rig-croak 4.2s ease-in-out infinite; animation-delay: calc(var(--blink) * -1); }
  .frogsvg .rig-armN { transform-origin: 40px 51px; }
  .frogsvg .rig-handN { transform-origin: 27px 86px; animation: rig-tap 3.3s ease-in-out infinite; animation-delay: calc(var(--breathe) * -1.5); }
  .frogsvg .rig-armF { transform-origin: 54px 60px; }
  .frogsvg .rig-thigh, .frogsvg .rig-thigh-front { transform-origin: 68px 72px; }
  .frogsvg .rig-foot { transform-origin: 61px 89px; }
  .frogsvg .rig-thigh { visibility: hidden; }
  .frogsvg .rig-thigh .rig-foot { visibility: visible; }
  @keyframes rig-head-idle { 0%,100% { transform: rotate(0); } 30% { transform: rotate(-3deg); } 60% { transform: rotate(2deg); } }
  @keyframes rig-croak { 0%,78%,100% { transform: scale(1); } 84% { transform: scale(1.25, 1.45); } 90% { transform: scale(1.05); } 94% { transform: scale(1.2, 1.35); } }
  @keyframes rig-tap { 0%,70%,100% { transform: rotate(0); } 75% { transform: rotate(-12deg); } 80% { transform: rotate(0); } 85% { transform: rotate(-12deg); } }

  /* HOP: crouch -> kick -> airborne tuck -> land. Synced to a 0.9s hop on the parent. */
  .hop .frogsvg .rig-thigh, .hop .frogsvg .rig-thigh-front { animation: rig-hop-thigh 0.9s ease-out; }
  .hop .frogsvg .rig-foot { animation: rig-hop-foot 0.9s ease-out; }
  .hop .frogsvg .rig-armN { animation: rig-hop-armN 0.9s ease-out; }
  .hop .frogsvg .rig-armF { animation: rig-hop-armF 0.9s ease-out; }
  .hop .frogsvg .rig-head { animation: rig-hop-head 0.9s ease-out; }
  .hop .frogsvg .rig-throat { animation: rig-hop-throat 0.9s ease-out; }
  @keyframes rig-hop-thigh { 0% { transform: none; } 12% { transform: rotate(-14deg); } 26% { transform: rotate(38deg) translate(4px, 6px); }
    55% { transform: rotate(18deg) translate(2px, 3px); } 80% { transform: rotate(-10deg); } 100% { transform: none; } }
  @keyframes rig-hop-foot { 0% { transform: none; } 12% { transform: rotate(10deg); } 26% { transform: rotate(-45deg) translate(6px, 2px); }
    55% { transform: rotate(-20deg); } 80% { transform: rotate(6deg); } 100% { transform: none; } }
  @keyframes rig-hop-armN { 0% { transform: none; } 12% { transform: rotate(8deg); } 26% { transform: rotate(-35deg); }
    50% { transform: rotate(60deg); } 80% { transform: rotate(18deg); } 100% { transform: none; } }
  @keyframes rig-hop-armF { 0% { transform: none; } 14% { transform: rotate(8deg); } 30% { transform: rotate(-30deg); }
    54% { transform: rotate(50deg); } 84% { transform: rotate(14deg); } 100% { transform: none; } }
  @keyframes rig-hop-head { 0% { transform: none; } 12% { transform: rotate(-8deg); } 30% { transform: rotate(10deg); }
    60% { transform: rotate(6deg); } 82% { transform: rotate(-6deg); } 100% { transform: none; } }
  @keyframes rig-hop-throat { 0%,80% { transform: scale(1); } 88% { transform: scale(1.35, 1.6); } 100% { transform: scale(1); } }

  /* CHEER: arms up, waving. */
  .cheer .frogsvg .rig-armN { animation: rig-cheer-N 0.6s ease-in-out infinite alternate; }
  .cheer .frogsvg .rig-armF { animation: rig-cheer-F 0.6s ease-in-out infinite alternate; }
  .cheer .frogsvg .rig-throat { animation: rig-croak 0.6s infinite; }
  @keyframes rig-cheer-N { from { transform: rotate(95deg); } to { transform: rotate(130deg); } }
  @keyframes rig-cheer-F { from { transform: rotate(120deg); } to { transform: rotate(90deg); } }

  /* SQUISH: something just landed on my head. */
  .squish .frogsvg .rig-head { animation: rig-squish-head 0.5s ease-out; }
  .squish .frogsvg .rig-armN, .squish .frogsvg .rig-armF { animation: rig-squish-arm 0.5s ease-out; }
  @keyframes rig-squish-head { 30% { transform: rotate(-12deg) translateY(3px); } }
  @keyframes rig-squish-arm { 30% { transform: rotate(-25deg); } }
`;

// Prize rosette pinned to the frog's side (opts.badge): this frog won a heat.
// A fair-ribbon rosette, not a crown or a medal, so it can't be mistaken for
// the crown hat or the leader's medal. Red on blue-ish frogs so it shows.
export function rosetteSVG(color) {
  const n = parseInt(color.slice(1), 16), r = n >> 16, g = (n >> 8) & 255, b = n & 255;
  const c = b > r + 40 && b > g - 20 ? "#d63031" : "#2f6fd6", dark = shade(c, -0.35);
  const ink = (d, w = 1.5) => `<path d="${d}" fill="none" stroke="${INK}" stroke-width="${w}" stroke-linecap="round" stroke-linejoin="round"/>`;
  const filled = (d, f, op = 0.85) => `<path d="${d}" fill="${PAPER}"/><path d="${d}" fill="${f}" opacity="${op}"/>${ink(d)}`;
  let ring = "", R = 6.2, k = 12;
  for (let i = 0; i <= k; i++) {
    const t = (i / k) * Math.PI * 2, x = (R * Math.cos(t)).toFixed(2), y = (R * Math.sin(t)).toFixed(2);
    ring += i ? ` A1.9 1.9 0 0 1 ${x} ${y}` : `M${x} ${y}`;
  }
  const star = Array.from({ length: 10 }, (_, i) => {
    const t = -Math.PI / 2 + (i * Math.PI) / 5, rr = i % 2 ? 1.1 : 2.6;
    return `${i ? "L" : "M"}${(rr * Math.cos(t)).toFixed(2)} ${(rr * Math.sin(t)).toFixed(2)}`;
  }).join(" ") + " Z";
  return `<g class="rosette" transform="translate(69 54) rotate(-8) scale(1.3)">
    ${filled("M-1.5 2 L-6.5 15 L-4 13.4 L-2.6 16.4 L1.6 3 Z", dark, 0.9)}${filled("M1.5 2 L6.5 15 L4 13.4 L2.6 16.4 L-1.6 3 Z", c, 0.9)}
    ${filled(ring + " Z", c)}
    <circle r="3.7" fill="#f7c600" stroke="${INK}" stroke-width="1.3"/>
    <path d="${star}" fill="#fff3c4" stroke="${INK}" stroke-width="0.7" stroke-linejoin="round"/>
  </g>`;
}

// A little buzzing fly, in the same ink style.
export function flySVG() {
  return `<svg viewBox="0 0 40 30" xmlns="http://www.w3.org/2000/svg">
    <g class="wings"><ellipse cx="15" cy="9" rx="6" ry="4" fill="#fff" fill-opacity="0.7" stroke="${INK}" stroke-width="1.4" transform="rotate(-25 15 9)"/>
    <ellipse cx="25" cy="9" rx="6" ry="4" fill="#fff" fill-opacity="0.7" stroke="${INK}" stroke-width="1.4" transform="rotate(25 25 9)"/></g>
    <ellipse cx="20" cy="16" rx="5" ry="6" fill="${INK}"/>
    <path d="M14 20 l-3 3 M26 20 l3 3 M15 23 l-2 3 M25 23 l2 3" stroke="${INK}" stroke-width="1.2" stroke-linecap="round"/>
    <path d="M3 4 q3 -3 5 0 M33 3 q3 3 5 0 M2 24 q2 2 4 0" stroke="${INK}" stroke-width="1" fill="none"/>
  </svg>`;
}
