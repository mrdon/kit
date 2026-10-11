import assert from "node:assert/strict";
import { test } from "node:test";
import sharp from "sharp";
import { brand, builtin, content, logo, photo, plain, service } from "./helpers.mjs";

test("copy check catches the mechanical tells and the tenant's own banned words", () => {
  const r = service.copyCheck({ ...content, summary: "Come for hoppy hour — it's AMAZING!" }, brand);
  const text = r.problems.join("\n");
  assert.match(text, /hoppy hour/);
  assert.match(text, /em or en dash/);
  assert.match(text, /exclamation/);
  assert.match(text, /ALL CAPS/);
  assert.match(text, /amazing/);
  assert.deepEqual(service.copyCheck(content, brand).problems, []);
  // Acronyms are not shouting.
  assert.deepEqual(service.copyCheck({ ...content, eyebrow: "NFL Sundays", title: "Broncos at the LA Chargers" }, brand).problems, []);
});

test("inspect reports size, orientation and a provenance verdict", async () => {
  const r = await service.store.inspect(await photo("i1"));
  assert.equal(r.width, 1600);
  assert.equal(r.orientation, "landscape");
  assert.equal(r.c2pa, "none");
  assert.ok(r.thumbnail.length > 100);
});

test("a photo whose XMP says it was generated is flagged", async () => {
  const { c2paVerdict } = await import("../dist/server.js").then(() => import("./c2pa.helper.mjs"));
  const xmp = Buffer.from(`\xff\xd8 ... <x:xmpmeta> Iptc4xmpExt:DigitalSourceType="http://cv.iptc.org/newscodes/digitalsourcetype/trainedAlgorithmicMedia" </x:xmpmeta>`, "latin1");
  assert.equal(c2paVerdict(xmp), "ai");
  assert.equal(c2paVerdict(Buffer.from("plain jpeg bytes")), "none");
});

test("a contact sheet holds up to twelve numbered thumbnails", async () => {
  const items = [];
  for (let i = 0; i < 5; i++) items.push({ image: await photo(`s${i}`), label: `${i + 1}. s${i}.jpg` });
  const png = await service.sheet(items, brand);
  assert.ok(png.length > 1000);
  const many = [];
  for (let i = 0; i < 13; i++) many.push({ image: await photo(`m${i}`), label: `${i}` });
  assert.throws(() => service.sheet(many, brand), /at most 12/);
});

test("a template whose meta is missing or malformed cannot pass the check", async () => {
  const noMeta = plain().replace(/export const meta[\s\S]*?};\n/, "");
  const r = await service.templateCheck(noMeta, brand, []);
  const all = Object.values(r.problems).flat().join("\n");
  assert.match(all, /export const meta/);
});

// A transparent PNG placed as a photo keeps its transparency: the ground
// shows through its padding instead of a black box. This is what an
// uploaded logo looks like when the model drops it in as a Photo.
test("a transparent image placed as a photo shows the ground through it", async () => {
  const png = await logo();
  const ref = { id: "attachment:x", source: "inline", data: png.toString("base64"), modified: "1" };
  const g = Object.keys(brand.grounds)[0];
  const source = `import React from "react";
import { grounds, Photo } from "@poster/kit";
export default function Poster({ format }) {
  return <div style={{ display: "flex", width: format.width, height: format.height, backgroundColor: grounds["${g}"].bg }}><Photo id="attachment:x" focusX={0.5} focusY={0.5} zoom={1} /></div>;
}`;
  const r = await service.render({ kind: "poster", source, format: "portrait", brand, photos: [ref] });
  assert.deepEqual(r.problems, []);
  const { data } = await sharp(Buffer.from(r.png, "base64")).raw().toBuffer({ resolveWithObject: true });
  const hex = brand.tokens[brand.grounds[g].bg];
  const want = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16));
  const got = [data[0], data[1], data[2]];
  assert.ok(got.every((v, i) => Math.abs(v - want[i]) < 10), `top-left pixel ${got} should be the ground ${hex}`);
});

// The same file as a logo variant renders with no "not mapped" warning.
test("an uploaded file can be a logo variant", async () => {
  const png = await logo();
  const ref = { id: "attachment:x", source: "inline", data: png.toString("base64"), modified: "1" };
  const withLogo = { ...brand, logos: { ...brand.logos, "attachment:x": ref } };
  const g = Object.keys(brand.grounds)[0];
  const source = `import React from "react";
import { grounds, Logo } from "@poster/kit";
export default function Poster({ format }) {
  return <div style={{ display: "flex", width: format.width, height: format.height, backgroundColor: grounds["${g}"].bg, padding: 100 }}><Logo variant="attachment:x" width={400} /></div>;
}`;
  const r = await service.render({ kind: "poster", source, format: "portrait", brand: withLogo, photos: [ref] });
  assert.deepEqual(r.problems, []);
  assert.ok(!r.warnings.some((w) => w.includes("not mapped")), r.warnings.join("; "));
});
