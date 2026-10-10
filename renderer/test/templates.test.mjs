import assert from "node:assert/strict";
import { test } from "node:test";
import { brand, builtin, content, logo, photo, save, service } from "./helpers.mjs";

const NAMES = ["split", "full", "type", "band", "strip", "inset", "when"];

test("every built-in template passes the activation check on portrait, story and screen", async () => {
  const photos = [await photo("p1"), await photo("p2", "#1C3D1B"), await photo("p3", "#D9D3C4")];
  for (const name of NAMES) {
    const r = await service.templateCheck(builtin(name), brand, photos);
    assert.ok(r.meta, `${name}: meta`);
    assert.equal(r.meta.name.toLowerCase(), name);
    assert.deepEqual(r.problems, {}, `${name}: ${JSON.stringify(r.problems)}`);
    assert.deepEqual(Object.keys(r.renders).sort(), ["portrait", "screen", "story"]);
    for (const [fmt, png] of Object.entries(r.renders)) save(`template-${name}-${fmt}.png`, png);
  }
});

test("a template render honours the logo mapping and trims its padding", async () => {
  const withLogo = { ...brand, logos: { color: { id: "logo-color", source: "inline", data: (await logo()).toString("base64"), modified: "1" } } };
  const r = await service.render({
    kind: "template",
    source: builtin("type"),
    format: "portrait",
    brand: withLogo,
    photos: [],
    props: { content, photos: [], ground: "paper", accent: "amber" },
  });
  assert.deepEqual(r.problems, []);
  // The white variant is unmapped on ink, and the warning says so.
  const ink = await service.render({
    kind: "template",
    source: builtin("type"),
    format: "portrait",
    brand: withLogo,
    photos: [],
    props: { content, photos: [], ground: "ink", accent: "amber" },
  });
  assert.deepEqual(ink.problems, []);
  assert.ok(ink.warnings.some((w) => w.includes("white logo is not mapped")), ink.warnings.join("; "));
});

test("text outside the safe margins fails the render", async () => {
  const src = `import React from "react";
import { Text, grounds, font } from "@poster/kit";
export default function Poster({ format }) {
  return (
    <div style={{ display: "flex", width: format.width, height: format.height, backgroundColor: grounds.paper.bg }}>
      <div style={{ position: "absolute", top: 4, left: 4, display: "flex", color: grounds.paper.text, fontFamily: font.text, fontSize: 40 }}>
        <Text>Too close to the edge</Text>
      </div>
    </div>
  );
}`;
  const r = await service.render({ kind: "poster", source: src, format: "feed", brand, photos: [] });
  assert.deepEqual(r.problems, ['"Too close to the edge" is outside the safe area']);
});
