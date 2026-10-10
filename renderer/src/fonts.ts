// Fonts for Satori: exactly the families and weights the brand names, as
// static TTFs. Google Fonts serves static per-weight TTFs to a browser old
// enough not to know WOFF2 or variable fonts, which is what Satori needs. A
// local font directory (POSTER_FONT_DIR) is checked first so tests run
// offline and a tenant can drop in a face Google does not carry.
import fs from "node:fs";
import path from "node:path";
import type { Brand } from "./types.ts";

// Safari 4: gets TTF, one file per weight.
const LEGACY_UA = "Mozilla/5.0 (Macintosh; U; Intel Mac OS X 10_5_8; en-us) AppleWebKit/531.9 (KHTML, like Gecko) Version/4.0.3 Safari/531.9";

import type { SatoriOptions } from "satori";

export type SatoriFont = SatoriOptions["fonts"][number] & { data: Buffer };
type Weight = SatoriFont["weight"];

const memo = new Map<string, Promise<SatoriFont[]>>();

export class FontError extends Error {}

const slug = (family: string) => family.replace(/\s+/g, "");

export function fontRequests(brand: Brand): Array<{ family: string; weight: number }> {
  const out: Array<{ family: string; weight: number }> = [];
  const seen = new Set<string>();
  for (const role of [brand.fonts.display, brand.fonts.text, brand.fonts.mono]) {
    for (const w of role.weights) {
      const k = `${role.family}|${w}`;
      if (seen.has(k)) continue;
      seen.add(k);
      out.push({ family: role.family, weight: w });
    }
  }
  return out;
}

// Loads every font the brand uses, cached in memory per process and on disk
// across processes. Throws FontError naming the family Google does not serve.
export function loadFonts(brand: Brand, cacheDir: string, localDir?: string): Promise<SatoriFont[]> {
  const reqs = fontRequests(brand);
  const key = reqs.map((r) => `${r.family}:${r.weight}`).join(",");
  let p = memo.get(key);
  if (!p) {
    p = Promise.all(reqs.map((r) => loadOne(r.family, r.weight, cacheDir, localDir)));
    memo.set(key, p);
    p.catch(() => memo.delete(key));
  }
  return p;
}

async function loadOne(family: string, weight: number, cacheDir: string, localDir?: string): Promise<SatoriFont> {
  const file = `${slug(family)}-${weight}.ttf`;
  if (localDir) {
    const local = path.join(localDir, file);
    if (fs.existsSync(local)) return { name: family, weight: weight as Weight, style: "normal", data: fs.readFileSync(local) };
  }
  const dir = path.join(cacheDir, "fonts");
  fs.mkdirSync(dir, { recursive: true });
  const cached = path.join(dir, file);
  if (fs.existsSync(cached)) return { name: family, weight: weight as Weight, style: "normal", data: fs.readFileSync(cached) };
  const data = await fetchGoogleFont(family, weight);
  fs.writeFileSync(cached, data);
  return { name: family, weight: weight as Weight, style: "normal", data };
}

async function fetchGoogleFont(family: string, weight: number): Promise<Buffer> {
  const css = `https://fonts.googleapis.com/css2?family=${encodeURIComponent(family).replace(/%20/g, "+")}:wght@${weight}`;
  const res = await fetch(css, { headers: { "User-Agent": LEGACY_UA } });
  if (!res.ok) throw new FontError(`Google Fonts does not serve "${family}" (HTTP ${res.status})`);
  const text = await res.text();
  const m = text.match(/url\((https:[^)]+\.ttf)\)/);
  if (!m) throw new FontError(`Google Fonts has no static TTF for "${family}" at weight ${weight}`);
  const font = await fetch(m[1]);
  if (!font.ok) throw new FontError(`downloading "${family}" ${weight} failed (HTTP ${font.status})`);
  return Buffer.from(await font.arrayBuffer());
}

// Reports which fonts can be loaded without throwing, for the admin page.
export async function checkFonts(brand: Brand, cacheDir: string, localDir?: string): Promise<string[]> {
  const problems: string[] = [];
  for (const r of fontRequests(brand)) {
    try {
      await loadOne(r.family, r.weight, cacheDir, localDir);
    } catch (e) {
      problems.push(e instanceof Error ? e.message : String(e));
    }
  }
  return problems;
}
