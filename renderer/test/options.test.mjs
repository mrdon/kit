import assert from "node:assert/strict";
import { test } from "node:test";
import { brand, builtin, content, photo, plain, save, service } from "./helpers.mjs";

const NAMES = ["badge", "calendar", "caption", "marquee", "mosaic", "pair", "ticket"];
const metas = {
  badge: { photos: { min: 1, max: 1 } },
  calendar: { photos: { min: 1, max: 1 } },
  caption: { photos: { min: 1, max: 1 } },
  marquee: { photos: { min: 1, max: 1 } },
  mosaic: { photos: { min: 3, max: 3 } },
  pair: { photos: { min: 2, max: 2 } },
  ticket: { photos: { min: 1, max: 1 } },
};
const templates = (weights = {}) =>
  NAMES.map((n) => ({ id: n, source: builtin(n), weight: weights[n] ?? 1, meta: { name: n, description: "", needs: ["eyebrow", "title"], ...metas[n] } }));

test("seven options spread template, ground and photo, and every one is a self-contained poster", async () => {
  const photos = [await photo("p1"), await photo("p2", "#1C3D1B"), await photo("p3", "#D9D3C4"), await photo("x", "#000000", "other")];
  const r = await service.options({
    content,
    brand,
    format: "portrait",
    photos,
    hero: { id: "p1", focusX: 0.8, focusY: 0.2, zoom: 1 },
    templates: templates(),
    count: 7,
  });
  assert.equal(r.options.length, 7, JSON.stringify(r.skipped));
  const combos = new Set(r.options.map((o) => `${o.templateId}/${o.ground}`));
  assert.equal(combos.size, 7);
  assert.ok(r.options.some((o) => o.ground === "ink") && r.options.some((o) => o.ground === "paper"));
  // The pool is the hero's folder: the photo from "other" never appears.
  for (const o of r.options) assert.ok(!o.photos.some((p) => p.id === "x"));
  // Photo templates take the hero and its set in turn; the hero leads one of them.
  assert.ok(r.options.some((o) => o.photos[0]?.id === "p1"));
  const marquee = r.options.find((o) => o.templateId === "marquee");
  assert.ok(marquee);
  assert.equal(marquee.photos.length, 1);
  // Each option re-renders from its own source alone.
  for (const o of r.options) {
    const again = await service.render({ kind: "poster", source: o.source, format: "portrait", brand, photos });
    assert.deepEqual(again.problems, [], o.templateId);
    save(`option-${o.templateId}-${o.ground}.png`, o.png);
  }
});

test("every layout needs a photo, so without a hero nothing is offered; weight orders the rest", async () => {
  const none = await service.options({ content, brand, format: "portrait", photos: [], templates: templates(), count: 7 });
  assert.equal(none.options.length, 0);
  const photos = [await photo("p1"), await photo("p2", "#1C3D1B"), await photo("p3", "#D9D3C4")];
  const r = await service.options({ content, brand, format: "portrait", photos, hero: { id: "p1", focusX: 0.5, focusY: 0.5, zoom: 1 }, templates: templates({ ticket: 5 }), count: 3 });
  assert.equal(r.options[0].templateId, "ticket");
});

test("templates already shown are left out when others remain", async () => {
  const r = await service.options({
    content,
    brand,
    format: "portrait",
    photos: [await photo("p1"), await photo("p2", "#1C3D1B"), await photo("p3", "#D9D3C4")],
    hero: { id: "p1", focusX: 0.5, focusY: 0.5, zoom: 1 },
    templates: templates(),
    count: 2,
    exclude: NAMES.filter((n) => n !== "caption").flatMap((n) => Object.keys(brand.grounds).map((g) => `${n}|${g}`)),
  });
  assert.deepEqual([...new Set(r.options.map((o) => o.templateId))], ["caption"]);
});

test("a request with no photo list at all does not fall over", async () => {
  // A Go nil slice arrives as JSON null; the service must not throw on it.
  const r = await service.options({ content: { ...content, details: null }, brand, format: "portrait", photos: null, templates: templates(), count: 2 });
  assert.ok(Array.isArray(r.options));
  const src = plain().replace(/export default function Template\(/, "function Template(").replace(/export const meta/, "const meta") +
    `\nconst content = ${JSON.stringify(content)};\nexport default function Poster({ format }) { return <Template content={content} format={format} photos={[]} ground="paper" accent="amber" />; }\n`;
  const again = await service.render({ kind: "poster", source: src, format: "portrait", brand, photos: null });
  assert.deepEqual(again.problems, []);
});

test("copy that fails the rules never reaches the generator", async () => {
  await assert.rejects(
    service.options({ content: { ...content, title: "Don't miss this!" }, brand, format: "portrait", photos: [], templates: templates(), count: 1 }),
    /copy fails/,
  );
});
