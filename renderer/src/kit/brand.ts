// The tenant's brand as the isolate sees it. The host sets `__brand` (the
// derived brand JSON) and `__logos` (variant -> aspect) on the global before
// the kit loads; this module turns them into the typed values templates use.

import type { Brand, Format } from "../types.ts";
export type { Format };

// The slice of the brand the host hands the isolate.
type BrandJSON = Pick<Brand, "tokens" | "grounds" | "accents" | "fonts" | "formats">;

declare const __brand: BrandJSON;
declare const __logos: Record<string, number>;

const brand: BrandJSON = __brand;

// Color tokens by name. Every color in a poster is one of these; the host
// rejects any other value, so there is no way to sneak a tint in.
export const tokens: Readonly<Record<string, string>> = Object.freeze({ ...brand.tokens });

export type Ground = { bg: string; text: string; label: string; rule: string; logo: string };
export type Accent = { fill: string; text: string };

const hex = (name: string) => {
  const v = brand.tokens[name];
  if (!v) throw new Error(`unknown color token: ${name}`);
  return v;
};

// Grounds and accents resolved to hex, keyed by their token name.
export const grounds: Readonly<Record<string, Ground>> = Object.freeze(
  Object.fromEntries(
    Object.entries(brand.grounds).map(([name, g]) => [
      name,
      { bg: hex(g.bg), text: hex(g.text), label: hex(g.label), rule: hex(g.rule), logo: g.logo },
    ]),
  ),
);

export const accents: Readonly<Record<string, Accent>> = Object.freeze(
  Object.fromEntries(
    Object.entries(brand.accents).map(([name, a]) => [name, { fill: hex(a.fill), text: hex(a.text) }]),
  ),
);

export const groundNames: readonly string[] = Object.keys(brand.grounds);
export const accentNames: readonly string[] = Object.keys(brand.accents);

// Font families. A template sets fontFamily to one of these three strings
// and nothing else.
export const font = Object.freeze({
  display: brand.fonts.display.family,
  text: brand.fonts.text.family,
  mono: brand.fonts.mono.family,
});

export const weights = Object.freeze({
  display: brand.fonts.display.weights[0] ?? 700,
  textRegular: Math.min(...brand.fonts.text.weights),
  textBold: Math.max(...brand.fonts.text.weights),
  mono: brand.fonts.mono.weights[0] ?? 500,
});

export const displayAdvance = brand.fonts.display.advance ?? 0.5;

export const formats: Readonly<Record<string, Format>> = Object.freeze({ ...brand.formats });

// Logo aspect ratios (width / height) for the variants the host resolved.
// A missing variant leaves its space empty rather than drawing anything.
export const logoAspects: Readonly<Record<string, number>> = Object.freeze({ ...(__logos ?? {}) });
