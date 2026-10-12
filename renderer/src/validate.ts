// Validates the element tree before Satori sees it. Two kinds of finding:
//
// Hard limits hold always. A poster shows real photos and the logo, never a
// picture made any other way: no <img>, no url() in any style, and inline
// SVG is vector shapes only (no <image>, <text>, <use>, <filter> or href),
// so nothing can smuggle in a raster or generated image.
//
// Brand rules (off-token colors, gradients, opacity, shadows, transforms, a
// second accent, an unapproved pairing, a number in the text face, banned
// copy) are the default. A render made with allowOffBrand, because a person
// asked for exactly that, reports them as warnings instead of refusing.
// Findings are plain sentences for the agent to act on.
import { checkText } from "./copy.ts";
import type { Brand, ImageRef, TreeNode } from "./types.ts";

type Elem = Exclude<TreeNode, string | number | null>;

const ALLOWED_TYPES = new Set(["div", "span", "p", "h1", "h2", "h3", "svg", "__photo", "__logo", "__fragment"]);

const SVG_SHAPES = new Set(["g", "rect", "circle", "ellipse", "line", "polyline", "polygon", "path"]);
// Gradient definitions are off-brand, not dangerous: they hold only stops.
const SVG_GRADIENT = new Set(["defs", "linearGradient", "radialGradient", "stop"]);
const SVG_ATTRS = new Set([
  "style", "width", "height", "viewBox", "preserveAspectRatio", "xmlns",
  "x", "y", "cx", "cy", "r", "rx", "ry", "x1", "y1", "x2", "y2", "d", "points",
  "fill", "fillRule", "stroke", "strokeWidth", "strokeLinecap", "strokeLinejoin",
  "strokeDasharray", "strokeDashoffset", "strokeMiterlimit",
]);
// Effects and gradient plumbing: allowed when off-brand is, never a way to
// reach outside the svg. Everything not in either list (href, filter,
// mask, clipPath, …) is refused.
const SVG_EFFECT_ATTRS = new Set([
  "opacity", "fillOpacity", "strokeOpacity", "transform", "id", "offset", "stopColor", "stopOpacity",
  "gradientUnits", "gradientTransform", "spreadMethod", "fx", "fy",
]);
const SVG_PAINT = new Set(["fill", "stroke"]);
const PAINT_KEYWORDS = new Set(["none", "currentColor"]);
const LOCAL_REF = /^url\(#[\w-]+\)$/;

const COLOR_PROPS = new Set(["color", "fill", "stroke", "background", "backgroundColor", "borderColor", "borderTopColor", "borderBottomColor", "borderLeftColor", "borderRightColor"]);
const BORDER_SHORTHANDS = new Set(["border", "borderTop", "borderBottom", "borderLeft", "borderRight"]);
const OFF_BRAND_PROPS: Record<string, string> = {
  opacity: "opacity",
  boxShadow: "a shadow",
  textShadow: "a shadow",
  backgroundImage: "a gradient",
  filter: "a filter",
  backdropFilter: "a filter",
  transform: "a transform",
  maskImage: "a mask",
  clipPath: "a clip path",
  fontStyle: "italic type",
  textDecoration: "underlined or struck text",
  textTransform: "a text transform (caps are a copy decision, not a style)",
};

export type ValidationContext = {
  brand: Brand;
  photos: Map<string, ImageRef>;
  logoVariants: Set<string>;
  // A person asked for something the brand guide rules out.
  allowOffBrand?: boolean;
};

type Inherited = { color?: string; bg?: string; fontFamily?: string; fontSize?: number };

export function validateTree(root: TreeNode, ctx: ValidationContext): { problems: string[]; warnings: string[] } {
  const v = new Validator(ctx);
  v.walk(root, { bg: undefined, color: undefined, fontFamily: undefined, fontSize: 16 });
  v.finish();
  return { problems: dedupe(v.problems), warnings: dedupe(v.warnings) };
}

const dedupe = (xs: string[]) => [...new Set(xs)];

class Validator {
  problems: string[] = [];
  warnings: string[] = [];
  private hexToToken = new Map<string, string>();
  private families: Set<string>;
  private accentCounts = new Map<string, number>();
  private pairs: Set<string>;
  private pairMin = new Map<string, number>();

  constructor(private ctx: ValidationContext) {
    for (const [name, hex] of Object.entries(ctx.brand.tokens)) this.hexToToken.set(hex.toLowerCase(), name);
    const f = ctx.brand.fonts;
    this.families = new Set([f.display.family, f.text.family, f.mono.family]);
    this.pairs = new Set(ctx.brand.pairs.map((p) => `${p.fg}/${p.bg}`));
    for (const p of ctx.brand.pairs) if (p.minSizePx) this.pairMin.set(`${p.fg}/${p.bg}`, p.minSizePx);
  }

  // A brand-guide finding: refused by default, a warning when a person
  // asked for it.
  private brand(msg: string) {
    if (this.ctx.allowOffBrand) this.warnings.push(`off-brand: ${msg}`);
    else this.problems.push(msg);
  }

  walk(node: TreeNode, inh: Inherited) {
    if (node === null) return;
    if (typeof node === "string" || typeof node === "number") {
      this.text(String(node), inh);
      return;
    }
    if (!ALLOWED_TYPES.has(node.type)) {
      this.problems.push(`<${node.type}> is not allowed; a poster may only use div, span, p, h1 to h3, svg shapes, Photo and Logo`);
      return;
    }
    if (node.type === "svg") {
      this.svg(node, inh);
      return;
    }
    if (node.type === "__photo") {
      const id = String(node.props.id ?? "");
      if (!this.ctx.photos.has(id)) this.problems.push(`photo "${id}" is not in the library photos this poster may use`);
      return;
    }
    if (node.type === "__logo") {
      const variant = String(node.props.variant ?? "");
      if (!this.ctx.logoVariants.has(variant)) this.warnings.push(`the ${variant} logo is not mapped; its space is left empty`);
      return;
    }
    const next = this.style(node, inh);
    for (const c of node.children) this.walk(c, next);
  }

  private style(node: Elem, inh: Inherited): Inherited {
    const style = (node.props.style ?? {}) as Record<string, unknown>;
    const next: Inherited = { ...inh };
    for (const [prop, raw] of Object.entries(style)) {
      if (raw === undefined || raw === null) continue;
      const value = String(raw);
      if (/url\(/i.test(value)) {
        this.problems.push(`${prop} loads an image; a poster's only pictures are library photos and the logo`);
      } else if (prop in OFF_BRAND_PROPS) {
        this.brand(`${OFF_BRAND_PROPS[prop]} is not in the brand (${prop})`);
      } else if (/gradient\(/i.test(value)) {
        this.brand(`${prop} uses a gradient, which the brand rules out`);
      } else if (COLOR_PROPS.has(prop)) {
        const token = this.token(value, prop);
        if (!token) continue;
        if (prop === "color") next.color = token;
        else if (prop === "background" || prop === "backgroundColor") next.bg = token;
        this.countAccent(token);
      } else if (BORDER_SHORTHANDS.has(prop)) {
        const hex = value.match(/#[0-9a-fA-F]{3,8}/)?.[0];
        if (!hex) this.brand(`${prop} must name a color token hex, got "${value}"`);
        else {
          const token = this.token(hex, prop);
          if (token) this.countAccent(token);
        }
      } else if (prop === "fontFamily") {
        // Not a brand preference: a family the brand does not load cannot render.
        if (!this.families.has(value)) this.problems.push(`fontFamily "${value}" is not one of the brand's families (${[...this.families].join(", ")})`);
        next.fontFamily = value;
      } else if (prop === "fontSize") {
        const n = parseFontSize(value, inh.fontSize ?? 16);
        if (n !== undefined) next.fontSize = n;
      }
    }
    return next;
  }

  // An svg and everything in it: vector shapes in token paint, no text, no
  // references outside itself.
  private svg(node: Elem, inh: Inherited) {
    if (SVG_GRADIENT.has(node.type)) this.brand(`<${node.type}> draws a gradient, which the brand rules out`);
    for (const [prop, raw] of Object.entries(node.props)) {
      if (raw === undefined || raw === null) continue;
      const value = String(raw).trim();
      if (SVG_EFFECT_ATTRS.has(prop)) {
        if (prop !== "id" && prop !== "offset") this.brand(`${prop} on <${node.type}> is not in the brand`);
      } else if (!SVG_ATTRS.has(prop)) {
        this.problems.push(`${prop} is not allowed on <${node.type}>; an svg shape takes geometry, fill and stroke only`);
      } else if (SVG_PAINT.has(prop) && !PAINT_KEYWORDS.has(value)) {
        if (LOCAL_REF.test(value)) this.brand(`${prop} on <${node.type}> uses a gradient, which the brand rules out`);
        else if (/url\(/i.test(value)) this.problems.push(`${prop} on <${node.type}> may only reference a gradient in the same svg`);
        else {
          const token = this.token(value, prop);
          if (token) this.countAccent(token);
        }
      }
    }
    const next = this.style(node, inh);
    for (const c of svgChildren(node.children)) {
      if (c === null) continue;
      if (typeof c === "string" || typeof c === "number") {
        if (String(c).trim()) this.problems.push(`text inside <svg> is not allowed; set words with Text`);
      } else if (!SVG_SHAPES.has(c.type) && !SVG_GRADIENT.has(c.type)) {
        this.problems.push(`<${c.type}> is not allowed inside <svg>; use g, rect, circle, ellipse, line, polyline, polygon or path`);
      } else {
        this.svg(c, next);
      }
    }
  }

  private token(value: string, prop: string): string | undefined {
    const t = this.hexToToken.get(value.trim().toLowerCase());
    if (!t) {
      this.brand(`${prop} "${value}" is not a brand color token; use one of ${Object.keys(this.ctx.brand.tokens).join(", ")}`);
      return undefined;
    }
    return t;
  }

  private countAccent(token: string) {
    if (token in this.ctx.brand.accents) this.accentCounts.set(token, (this.accentCounts.get(token) ?? 0) + 1);
  }

  private text(text: string, inh: Inherited) {
    if (!text.trim()) return;
    const mono = this.ctx.brand.fonts.mono.family;
    if (/\d/.test(text) && inh.fontFamily !== mono) {
      this.brand(`"${text.trim().slice(0, 30)}" has a number outside the mono family; wrap copy in <Text> so figures are set in ${mono}`);
    }
    if (inh.color && inh.bg) {
      const key = `${inh.color}/${inh.bg}`;
      if (!this.pairs.has(key)) {
        this.brand(`${inh.color} text on ${inh.bg} is not an approved pairing ("${text.trim().slice(0, 30)}")`);
      } else {
        const min = this.pairMin.get(key);
        if (min && (inh.fontSize ?? 16) < min) this.brand(`${inh.color} on ${inh.bg} needs ${min}px or larger type ("${text.trim().slice(0, 30)}")`);
      }
    }
    const r = checkText(`"${text.trim().slice(0, 30)}"`, text, this.ctx.brand.bannedWords ?? []);
    for (const p of r.problems) this.brand(p);
    this.warnings.push(...r.warnings);
  }

  finish() {
    const rules = this.ctx.brand.accentRules;
    const used = [...this.accentCounts.entries()].filter(([, n]) => n > 0);
    for (const [name, n] of used) {
      if (n > rules.maxElements) this.brand(`${name} appears on ${n} elements; the brand allows ${rules.maxElements}`);
    }
    if (!rules.together && used.length > 1) {
      this.brand(`${used.map(([n]) => n).join(" and ")} are both used; the brand allows one accent per composition`);
    }
  }
}

function svgChildren(children: TreeNode[]): TreeNode[] {
  return children.flatMap((c) => (c !== null && typeof c === "object" && c.type === "__fragment" ? svgChildren(c.children) : [c]));
}

function parseFontSize(value: string, parent: number): number | undefined {
  const n = parseFloat(value);
  if (Number.isNaN(n)) return undefined;
  if (/em$/.test(value)) return n * parent;
  return n;
}

// A short description of the tree for a reply: top-level structure only.
export function summarizeTree(root: TreeNode): string {
  const parts: string[] = [];
  const visit = (n: TreeNode, depth: number) => {
    if (n === null || typeof n !== "object") return;
    if (depth <= 2 && n.type !== "__fragment") {
      const style = (n.props.style ?? {}) as Record<string, unknown>;
      const bits = [n.type];
      if (style.backgroundColor || style.background) bits.push(`bg=${style.backgroundColor ?? style.background}`);
      if (n.type === "__photo") bits.push(`photo=${n.props.id}`);
      parts.push(`${"  ".repeat(depth)}${bits.join(" ")}`);
    }
    for (const c of n.children) visit(c, depth + 1);
  };
  visit(root, 0);
  return parts.join("\n");
}
