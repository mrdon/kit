import assert from "node:assert/strict";
import { test } from "node:test";
import { brand, builtin, content, photo, service } from "./helpers.mjs";

test("copy check catches the mechanical tells and the tenant's own banned words", () => {
  const r = service.copyCheck({ ...content, summary: "Come for hoppy hour — it's AMAZING!" }, brand);
  const text = r.problems.join("\n");
  assert.match(text, /hoppy hour/);
  assert.match(text, /em or en dash/);
  assert.match(text, /exclamation/);
  assert.match(text, /ALL CAPS/);
  assert.match(text, /amazing/);
  assert.deepEqual(service.copyCheck(content, brand).problems, []);
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
  const noMeta = builtin("type").replace(/export const meta[\s\S]*?};\n/, "");
  const r = await service.templateCheck(noMeta, brand, []);
  const all = Object.values(r.problems).flat().join("\n");
  assert.match(all, /export const meta/);
});
