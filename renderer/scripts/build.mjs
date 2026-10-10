#!/usr/bin/env node
// Builds dist/: the host server as one ESM file (native modules stay
// external), plus the two CommonJS bundles that run inside the isolate.
import { build } from "esbuild";
import fs from "node:fs";
import path from "node:path";

const root = path.resolve(path.dirname(new URL(import.meta.url).pathname), "..");
const dist = path.join(root, "dist");
fs.mkdirSync(dist, { recursive: true });

await build({
  entryPoints: [path.join(root, "src/server.ts")],
  outfile: path.join(dist, "server.js"),
  bundle: true,
  platform: "node",
  format: "esm",
  target: "node22",
  external: ["sharp", "@resvg/resvg-js", "isolated-vm", "esbuild", "satori"],
  banner: { js: "import { createRequire } from 'node:module'; const require = createRequire(import.meta.url);" },
  logLevel: "warning",
});

// The react shim and the kit, as CJS strings the sandbox loader evaluates.
// `react` is external to the kit bundle so the kit's import resolves to the
// shim module inside the isolate rather than a second copy.
await build({
  entryPoints: [path.join(root, "src/kit/react.ts")],
  outfile: path.join(dist, "react.cjs"),
  bundle: true,
  platform: "neutral",
  format: "cjs",
  target: "es2022",
  logLevel: "warning",
});
await build({
  entryPoints: [path.join(root, "src/kit/index.ts")],
  outfile: path.join(dist, "kit.cjs"),
  bundle: true,
  platform: "neutral",
  format: "cjs",
  target: "es2022",
  external: ["react"],
  jsx: "transform",
  jsxFactory: "React.createElement",
  jsxFragment: "React.Fragment",
  logLevel: "warning",
});
console.log("renderer: built dist/server.js, dist/react.cjs, dist/kit.cjs");
