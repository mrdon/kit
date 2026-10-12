import assert from "node:assert/strict";
import { test } from "node:test";
import { brand, builtin, content, photo, plain, service } from "./helpers.mjs";

// A minimal poster: the plain fixture with the content inlined, so each
// test can swap one thing and watch the validator catch it.
const posterFrom = (template, extra = "") =>
  template.replace(/export default function Template\(/, "function Template(").replace(/export const meta/, "const meta") +
  `
const content = ${JSON.stringify(content)};
${extra}
export default function Poster({ format }) {
  return <Template content={content} format={format} photos={[]} ground="paper" accent="amber" />;
}
`;

const render = (source, photos = []) => service.render({ kind: "poster", source, format: "portrait", brand, photos });

test("a poster with the content inlined renders clean", async () => {
  const r = await render(posterFrom(plain()));
  assert.deepEqual(r.problems, []);
  assert.ok(r.png.length > 1000);
});

test("imports outside react and @poster/kit are refused before running", async () => {
  await assert.rejects(render(`import fs from "node:fs";\n` + posterFrom(plain())), /import of "node:fs" is not allowed/);
});

test("the isolate has no host objects", async () => {
  const src = posterFrom(plain(), `const x = typeof fetch === "undefined" && typeof process === "undefined" && typeof require === "function"; if (!x) throw new Error("host leak");`);
  const r = await render(src);
  assert.deepEqual(r.problems, []);
  await assert.rejects(render(posterFrom(plain(), `fetch("https://example.com");`)), /fetch is not defined/);
});

test("runaway code hits the timeout", async () => {
  await assert.rejects(render(posterFrom(plain(), `for (;;) {}`)), /longer than 2 seconds/);
});

test("drawing elements, off-token colors, gradients and a second accent are rejected", async () => {
  const bad = (jsx) =>
    `import React from "react";
import { grounds, accents, font, type TemplateProps } from "@poster/kit";
export default function Poster({ format }) {
  return <div style={{ display: "flex", width: format.width, height: format.height, backgroundColor: grounds.paper.bg }}>${jsx}</div>;
}`;
  let r = await render(bad(`<img src="https://example.com/x.png" width={10} height={10} />`));
  assert.ok(r.problems.some((p) => p.includes("<img> is not allowed")), r.problems.join("; "));

  r = await render(bad(`<div style={{ display: "flex", backgroundColor: "#ff00ff", width: 10, height: 10 }} />`));
  assert.ok(r.problems.some((p) => p.includes("not a brand color token")), r.problems.join("; "));

  r = await render(bad(`<div style={{ display: "flex", backgroundImage: "linear-gradient(red, blue)", width: 10, height: 10 }} />`));
  assert.ok(r.problems.some((p) => p.includes("gradient")), r.problems.join("; "));

  r = await render(bad(`<div style={{ display: "flex", backgroundColor: accents.amber.fill, width: 10, height: 10 }} /><div style={{ display: "flex", backgroundColor: accents.amber.fill, width: 10, height: 10 }} />`));
  assert.ok(r.problems.some((p) => p.includes("amber appears on 2 elements")), r.problems.join("; "));

  r = await render(bad(`<div style={{ display: "flex", backgroundColor: accents.amber.fill, width: 10, height: 10 }} /><div style={{ display: "flex", backgroundColor: accents.ember.fill, width: 10, height: 10 }} />`));
  assert.ok(r.problems.some((p) => p.includes("both used")), r.problems.join("; "));

  r = await render(bad(`<div style={{ display: "flex", color: grounds.paper.text, fontFamily: font.text }}>Doors at 7pm</div>`));
  assert.ok(r.problems.some((p) => p.includes("number outside the mono family")), r.problems.join("; "));

  r = await render(bad(`<div style={{ display: "flex", color: accents.amber.fill, fontFamily: font.text }}>Hello</div>`));
  assert.ok(r.problems.some((p) => p.includes("not an approved pairing")), r.problems.join("; "));

  r = await render(bad(`<div style={{ display: "flex", color: grounds.paper.text, fontFamily: "Comic Sans" }}>Hello</div>`));
  assert.ok(r.problems.some((p) => p.includes("not one of the brand's families")), r.problems.join("; "));

  r = await render(bad(`<div style={{ display: "flex", color: grounds.paper.text, fontFamily: font.text }}>Don't miss our amazing night</div>`));
  assert.ok(r.problems.some((p) => p.includes('banned "amazing"')), r.problems.join("; "));
});

// The shared bare poster the svg tests draw into.
const shapes = (jsx) =>
  `import React from "react";
import { grounds, accents } from "@poster/kit";
export default function Poster({ format }) {
  return <div style={{ display: "flex", width: format.width, height: format.height, backgroundColor: grounds.paper.bg }}>${jsx}</div>;
}`;

test("simple svg shapes in token colors render", async () => {
  const r = await render(
    shapes(`<svg width="200" height="200" viewBox="0 0 200 200">
      <circle cx="100" cy="100" r="80" fill={grounds.paper.text} />
      <path d="M20 180L180 20" stroke={grounds.paper.text} strokeWidth="6" fill="none" />
      <polygon points="100,10 120,80 190,80" fill={accents.amber.fill} />
    </svg>`),
  );
  assert.deepEqual(r.problems, []);
  assert.ok(r.png.length > 1000);
});

test("svg cannot carry an image, text or an outside reference", async () => {
  let r = await render(shapes(`<svg width="10" height="10"><image href="https://example.com/x.png" width="10" height="10" /></svg>`));
  assert.ok(r.problems.some((p) => p.includes("<image> is not allowed inside <svg>")), r.problems.join("; "));
  r = await render(shapes(`<svg width="10" height="10"><text>Hi</text></svg>`));
  assert.ok(r.problems.some((p) => p.includes("<text> is not allowed inside <svg>")), r.problems.join("; "));
  r = await render(shapes(`<svg width="10" height="10"><rect width="10" height="10" fill="url(https://example.com/x.png)" /></svg>`));
  assert.ok(r.problems.some((p) => p.includes("may only reference a gradient")), r.problems.join("; "));
  r = await render(shapes(`<svg width="10" height="10"><rect width="10" height="10" fill="#ff00ff" /></svg>`));
  assert.ok(r.problems.some((p) => p.includes("not a brand color token")), r.problems.join("; "));
});

test("off-brand renders turn brand findings into warnings, never the hard limits", async () => {
  const offBrand = (source) => service.render({ kind: "poster", source, format: "portrait", brand, photos: [], allowOffBrand: true });
  let r = await offBrand(
    shapes(`<div style={{ display: "flex", width: 100, height: 100, opacity: 0.5, backgroundImage: "linear-gradient(#000, #fff)" }} />
      <svg width="100" height="100"><defs><linearGradient id="g"><stop offset="0" stopColor="#ff00ff" /></linearGradient></defs><rect width="100" height="100" fill="url(#g)" opacity="0.4" /></svg>`),
  );
  assert.deepEqual(r.problems, []);
  assert.ok(r.warnings.some((w) => w.startsWith("off-brand: ") && w.includes("gradient")), r.warnings.join("; "));
  assert.ok(r.png.length > 1000);

  r = await offBrand(shapes(`<div style={{ display: "flex", width: 100, height: 100, backgroundImage: "url(https://example.com/x.png)" }} />`));
  assert.ok(r.problems.some((p) => p.includes("loads an image")), r.problems.join("; "));
  r = await offBrand(shapes(`<img src="https://example.com/x.png" width={10} height={10} />`));
  assert.ok(r.problems.some((p) => p.includes("<img> is not allowed")), r.problems.join("; "));
});

test("an unknown photo id is a validation error, and a known one renders", async () => {
  const src = `import React from "react";
import { Photo, grounds } from "@poster/kit";
export default function Poster({ format }) {
  return <div style={{ display: "flex", width: format.width, height: format.height, backgroundColor: grounds.paper.bg }}><Photo id="PHOTO" style={{ width: 400, height: 300 }} /></div>;
}`;
  let r = await render(src.replace("PHOTO", "nope"), [await photo("p1")]);
  assert.ok(r.problems.some((p) => p.includes('photo "nope" is not in the library')), r.problems.join("; "));
  r = await render(src.replace("PHOTO", "p1"), [await photo("p1")]);
  assert.deepEqual(r.problems, []);
});
