import assert from "node:assert/strict";
import { test } from "node:test";
import { brand, builtin, content, photo, save, service } from "./helpers.mjs";

const NAMES = ["split", "full", "type", "band", "strip", "inset", "when"];
const metas = {
  split: { photos: { min: 1, max: 1 } },
  full: { photos: { min: 1, max: 1 } },
  type: { photos: { min: 0, max: 0 } },
  band: { photos: { min: 1, max: 1 } },
  strip: { photos: { min: 3, max: 3 } },
  inset: { photos: { min: 1, max: 1 } },
  when: { photos: { min: 0, max: 0 } },
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
  // The strip template got three photos from the set.
  const strip = r.options.find((o) => o.templateId === "strip");
  assert.ok(strip);
  assert.equal(strip.photos.length, 3);
  // Each option re-renders from its own source alone.
  for (const o of r.options) {
    const again = await service.render({ kind: "poster", source: o.source, format: "portrait", brand, photos });
    assert.deepEqual(again.problems, [], o.templateId);
    save(`option-${o.templateId}-${o.ground}.png`, o.png);
  }
});

test("without a hero only the type-only templates are offered, and weight orders them", async () => {
  const r = await service.options({ content, brand, format: "portrait", photos: [], templates: templates({ when: 5 }), count: 7 });
  assert.equal(r.options.length, 4);
  assert.equal(r.options[0].templateId, "when");
  assert.deepEqual([...new Set(r.options.map((o) => o.templateId))].sort(), ["type", "when"]);
});

test("templates already shown are left out when others remain", async () => {
  const r = await service.options({
    content,
    brand,
    format: "portrait",
    photos: [],
    templates: templates(),
    count: 2,
    exclude: ["type"],
  });
  assert.deepEqual([...new Set(r.options.map((o) => o.templateId))], ["when"]);
});

test("copy that fails the rules never reaches the generator", async () => {
  await assert.rejects(
    service.options({ content: { ...content, title: "Don't miss this!" }, brand, format: "portrait", photos: [], templates: templates(), count: 1 }),
    /copy fails/,
  );
});
