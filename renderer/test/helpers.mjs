// Shared test setup: an offline service (local fonts, temp cache) and
// generated photos so no test touches the network.
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import sharp from "sharp";

const here = path.dirname(new URL(import.meta.url).pathname);
process.env.POSTER_RENDERER_NO_LISTEN = "1";
process.env.POSTER_FONT_DIR = path.join(here, "fonts");
process.env.POSTER_RENDERER_CACHE_DIR = fs.mkdtempSync(path.join(os.tmpdir(), "kit-renderer-test-"));

export const { service } = await import("../dist/server.js");
export const brand = JSON.parse(fs.readFileSync(path.join(here, "brand.gravity.json"), "utf8"));
export const builtinsDir = path.resolve(here, "../../internal/apps/posters/builtins");
export const builtin = (name) => fs.readFileSync(path.join(builtinsDir, `${name}.tsx`), "utf8");
export const outDir = path.join(here, "out");

export const content = {
  eyebrow: "Thu Oct 15",
  title: "Vinyl night",
  summary: "Bring a record, we play a side. The good speakers come out and the lights go down at 7.",
  details: [
    { label: "When", value: "7 to 10pm" },
    { label: "Price", value: "Free" },
  ],
  action: "Records welcome, requests tolerated",
};

// A flat photo in a token color with a brighter block where the focus is,
// so a crop test can tell a focused render from a centered one.
export async function photo(id, color = "#6B8F4E", folder = "set", w = 1600, h = 1200) {
  const buf = await sharp({ create: { width: w, height: h, channels: 3, background: color } })
    .composite([{ input: await sharp({ create: { width: 200, height: 200, channels: 3, background: "#F5A81C" } }).png().toBuffer(), left: w - 260, top: 60 }])
    .jpeg()
    .toBuffer();
  return { id, source: "inline", data: buf.toString("base64"), modified: "1", folder };
}

export function logo() {
  // A logo PNG with transparent padding, which the store trims.
  return sharp({ create: { width: 400, height: 300, channels: 4, background: { r: 0, g: 0, b: 0, alpha: 0 } } })
    .composite([{ input: Buffer.from(`<svg width="400" height="300"><rect x="40" y="60" width="320" height="180" fill="#1C3D1B"/></svg>`), left: 0, top: 0 }])
    .png()
    .toBuffer();
}

export function save(name, pngBase64) {
  if (!process.env.KEEP_RENDERS) return;
  fs.mkdirSync(outDir, { recursive: true });
  fs.writeFileSync(path.join(outDir, name), Buffer.from(pngBase64, "base64"));
}
