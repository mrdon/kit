// Runs poster and template code. The code is written by an LLM, so it is
// compiled (not bundled: nothing outside the allowlist can be imported) and
// run in a V8 isolate with a memory cap, a timeout, and no host objects at
// all: no filesystem, no network, no timers. Its only output is the element
// tree as JSON.
import fs from "node:fs";
import path from "node:path";
import { transformSync } from "esbuild";
import ivm from "isolated-vm";
import type { Brand, TemplateMeta, TemplateProps, TreeNode } from "./types.ts";

const ALLOWED_IMPORTS = new Set(["react", "@poster/kit"]);
const MEMORY_MB = 64;
const TIMEOUT_MS = 2000;

const here = path.dirname(new URL(import.meta.url).pathname);
let bundles: { react: string; kit: string } | undefined;

// The kit and react shim are built once (scripts/build.mjs) into CJS files
// next to the server. Read lazily so tests can point at a fresh build.
function loadBundles() {
  if (!bundles) {
    bundles = {
      react: fs.readFileSync(path.join(here, "react.cjs"), "utf8"),
      kit: fs.readFileSync(path.join(here, "kit.cjs"), "utf8"),
    };
  }
  return bundles;
}

export class SourceError extends Error {}

// Compile TSX to CJS and check its imports. esbuild's transform does not
// resolve modules, so the output's require() calls are exactly the source's
// imports; anything off the allowlist is refused before a byte of it runs.
export function compile(source: string): string {
  let js: string;
  try {
    js = transformSync(source, {
      loader: "tsx",
      format: "cjs",
      target: "es2022",
      jsx: "transform",
      jsxFactory: "React.createElement",
      jsxFragment: "React.Fragment",
    }).code;
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e);
    throw new SourceError(`the source does not compile: ${firstLine(msg)}`);
  }
  // esbuild drops imports TypeScript considers unused, so the source's own
  // import lines are checked too: the error should name the import whether
  // or not the code got as far as using it.
  const specifiers = [
    ...js.matchAll(/require\(\s*["']([^"']+)["']\s*\)/g),
    ...source.matchAll(/^\s*import\s+(?:[^'"]*?\sfrom\s+)?["']([^"']+)["']/gm),
    ...source.matchAll(/^\s*export\s+[^'"]*?\sfrom\s+["']([^"']+)["']/gm),
  ];
  for (const m of specifiers) {
    if (!ALLOWED_IMPORTS.has(m[1])) {
      throw new SourceError(`import of "${m[1]}" is not allowed; only react and @poster/kit may be imported`);
    }
  }
  if (/\bimport\s*\(/.test(source)) throw new SourceError("dynamic import() is not allowed");
  return js;
}

const firstLine = (s: string) => s.split("\n")[0].trim();

// The loader that runs inside the isolate: a three-module CommonJS world.
const LOADER = `
const __mods = Object.create(null);
function __require(name) {
  if (!(name in __mods)) throw new Error("import of " + name + " is not allowed");
  return __mods[name];
}
function __define(name, code) {
  const module = { exports: {} };
  new Function("require", "module", "exports", code)(__require, module, module.exports);
  __mods[name] = module.exports;
}
function __run(code, propsJson) {
  __define("__poster", code);
  const mod = __mods["__poster"];
  const fn = mod.default ?? mod;
  if (typeof fn !== "function") throw new Error("the source must default-export a function");
  const tree = fn(JSON.parse(propsJson));
  return JSON.stringify({ tree, meta: mod.meta ?? null });
}
`;

export type RunResult = { tree: TreeNode; meta: TemplateMeta | null };

// Runs compiled code with props and returns its tree. logoAspects are the
// width/height ratios of the logo variants the host resolved, which the kit's
// Logo needs to reserve the right box.
export function run(
  compiled: string,
  props: TemplateProps | { format: TemplateProps["format"] },
  brand: Brand,
  logoAspects: Record<string, number>,
): RunResult {
  const isolate = new ivm.Isolate({ memoryLimit: MEMORY_MB });
  try {
    const context = isolate.createContextSync();
    const global = context.global;
    global.setSync("global", global.derefInto());
    const brandForKit = {
      tokens: brand.tokens,
      grounds: brand.grounds,
      accents: brand.accents,
      fonts: brand.fonts,
      formats: brand.formats,
    };
    context.evalSync(`globalThis.__brand = ${JSON.stringify(brandForKit)}; globalThis.__logos = ${JSON.stringify(logoAspects)};`);
    context.evalSync(LOADER);
    const b = loadBundles();
    const define = context.global.getSync("__define", { reference: true }) as ivm.Reference<(name: string, code: string) => void>;
    define.applySync(undefined, ["react", b.react], { timeout: TIMEOUT_MS });
    define.applySync(undefined, ["@poster/kit", b.kit], { timeout: TIMEOUT_MS });
    const runFn = context.global.getSync("__run", { reference: true }) as ivm.Reference<(code: string, props: string) => string>;
    const out = runFn.applySync(undefined, [compiled, JSON.stringify(props)], { timeout: TIMEOUT_MS });
    const parsed = JSON.parse(String(out)) as RunResult;
    return parsed;
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e);
    if (/Script execution timed out/i.test(msg)) throw new SourceError("the source took longer than 2 seconds to run");
    if (/memory|heap/i.test(msg) && isolate.isDisposed) throw new SourceError("the source used more than 64MB of memory");
    throw new SourceError(`the source failed while running: ${firstLine(msg)}`);
  } finally {
    if (!isolate.isDisposed) isolate.dispose();
  }
}

// Validates a template's meta export against the contract.
export function checkMeta(meta: TemplateMeta | null): string[] {
  const problems: string[] = [];
  if (!meta || typeof meta !== "object") return ["the template must `export const meta = { name, description, photos: { min, max }, needs }`"];
  if (typeof meta.name !== "string" || !meta.name.trim()) problems.push("meta.name must be a short name");
  if (typeof meta.description !== "string") problems.push("meta.description must describe the layout in a sentence");
  const p = meta.photos;
  if (!p || typeof p.min !== "number" || typeof p.max !== "number" || p.min < 0 || p.max < p.min) {
    problems.push("meta.photos must be { min, max } with 0 <= min <= max");
  }
  if (!Array.isArray(meta.needs) || meta.needs.some((n) => typeof n !== "string")) {
    problems.push("meta.needs must be a list of content field names the template cannot do without");
  }
  return problems;
}
