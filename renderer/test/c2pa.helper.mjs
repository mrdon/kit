// The c2pa module is bundled into dist/server.js; this re-exports it from
// source for a direct unit test (node runs .ts via the esbuild transform in
// the test build, so import the built copy instead).
import { transformSync } from "esbuild";
import fs from "node:fs";
import path from "node:path";

const here = path.dirname(new URL(import.meta.url).pathname);
const src = fs.readFileSync(path.join(here, "../src/c2pa.ts"), "utf8");
const js = transformSync(src, { loader: "ts", format: "esm" }).code;
const mod = await import(`data:text/javascript;base64,${Buffer.from(js).toString("base64")}`);
export const { c2paVerdict } = mod;
