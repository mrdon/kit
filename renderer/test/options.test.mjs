import assert from "node:assert/strict";
import { test } from "node:test";
import { brand, builtin, content, photo, save, service } from "./helpers.mjs";

const NAMES = ["badge", "calendar", "caption", "corners", "marquee", "masthead", "twotone"];
const metas = {
  badge: { photos: { min: 0, max: 1 } },
  calendar: { photos: { min: 0, max: 1 } },
  caption: { photos: { min: 1, max: 1 } },
  corners: { photos: { min: 0, max: 1 } },
  marquee: { photos: { min: 1, max: 1 } },
  masthead: { photos: { min: 0, max: 0 } },
  twotone: { photos: { min: 0, max: 1 } },
};
// The templates that stand without a photo.
const TYPE_ONLY = NAMES.filter((n) => metas[n].photos.min === 0);
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
  // A photo template got the hero.
  const marquee = r.options.find((o) => o.templateId === "marquee");
  assert.ok(marquee);
  assert.equal(marquee.photos[0].id, "p1");
  // Each option re-renders from its own source alone.
  for (const o of r.options) {
    const again = await service.render({ kind: "poster", source: o.source, format: "portrait", brand, photos });
    assert.deepEqual(again.problems, [], o.templateId);
    save(`option-${o.templateId}-${o.ground}.png`, o.png);
  }
});

test("without a hero only the templates that stand without a photo are offered, and weight orders them", async () => {
  const r = await service.options({ content, brand, format: "portrait", photos: [], templates: templates({ masthead: 5 }), count: 7 });
  assert.equal(r.options.length, 7, JSON.stringify(r.skipped));
  assert.equal(r.options[0].templateId, "masthead");
  for (const o of r.options) assert.ok(TYPE_ONLY.includes(o.templateId), o.templateId);
});

test("templates already shown are left out when others remain", async () => {
  const r = await service.options({
    content,
    brand,
    format: "portrait",
    photos: [],
    templates: templates(),
    count: 2,
    exclude: TYPE_ONLY.filter((n) => n !== "twotone").flatMap((n) => Object.keys(brand.grounds).map((g) => `${n}|${g}`)),
  });
  assert.deepEqual([...new Set(r.options.map((o) => o.templateId))], ["twotone"]);
});

test("a request with no photo list at all still produces type-only options", async () => {
  // A Go nil slice arrives as JSON null; the service must not fall over on it.
  const req = { content: { ...content, details: null }, brand, format: "portrait", photos: null, templates: templates(), count: 2 };
  const r = await service.options(req);
  assert.ok(r.options.length > 0, "no options");
  const again = await service.render({ kind: "poster", source: r.options[0].source, format: "portrait", brand, photos: null });
  assert.deepEqual(again.problems, []);
});

test("copy that fails the rules never reaches the generator", async () => {
  await assert.rejects(
    service.options({ content: { ...content, title: "Don't miss this!" }, brand, format: "portrait", photos: [], templates: templates(), count: 1 }),
    /copy fails/,
  );
});
