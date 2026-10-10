// Validates the element tree before Satori sees it. Every rule here is a
// brand-guide rule a layout could break by writing code: an off-token color,
// a third typeface, a gradient, a second accent, a number in the text face,
// a drawing element. Problems are plain sentences for the agent to act on.
import { checkText } from "./copy.ts";
import type { Brand, ImageRef, TreeNode } from "./types.ts";

type Elem = Exclude<TreeNode, string | number | null>;

const ALLOWED_TYPES = new Set(["div", "span", "p", "h1", "h2", "h3", "__photo", "__logo", "__fragment"]);

const COLOR_PROPS = new Set(["color", "background", "backgroundColor", "borderColor", "borderTopColor", "borderBottomColor", "borderLeftColor", "borderRightColor"]);
const BORDER_SHORTHANDS = new Set(["border", "borderTop", "borderBottom", "borderLeft", "borderRight"]);
const FORBIDDEN_PROPS: Record<string, string> = {
  opacity: "opacity",
  boxShadow: "a shadow",
  textShadow: "a shadow",
  backgroundImage: "a background image or gradient",
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

  walk(node: TreeNode, inh: Inherited) {
    if (node === null) return;
    if (typeof node === "string" || typeof node === "number") {
      this.text(String(node), inh);
      return;
    }
    if (!ALLOWED_TYPES.has(node.type)) {
      this.problems.push(`<${node.type}> is not allowed; a poster may only use div, span, p, h1 to h3, Photo and Logo`);
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
      if (prop in FORBIDDEN_PROPS) {
        this.problems.push(`${FORBIDDEN_PROPS[prop]} is not allowed (${prop})`);
        continue;
      }
      const value = String(raw);
      if (COLOR_PROPS.has(prop)) {
        const token = this.token(value, prop);
        if (!token) continue;
        if (prop === "color") next.color = token;
        else if (prop === "background" || prop === "backgroundColor") next.bg = token;
        this.countAccent(token);
      } else if (BORDER_SHORTHANDS.has(prop)) {
        const hex = value.match(/#[0-9a-fA-F]{3,8}/)?.[0];
        if (!hex) this.problems.push(`${prop} must name a color token hex, got "${value}"`);
        else {
          const token = this.token(hex, prop);
          if (token) this.countAccent(token);
        }
      } else if (prop === "fontFamily") {
        if (!this.families.has(value)) this.problems.push(`fontFamily "${value}" is not one of the brand's families (${[...this.families].join(", ")})`);
        next.fontFamily = value;
      } else if (prop === "fontSize") {
        const n = parseFontSize(value, inh.fontSize ?? 16);
        if (n !== undefined) next.fontSize = n;
      } else if (/gradient|url\(/i.test(value)) {
        this.problems.push(`${prop} uses a gradient or image, which the brand forbids`);
      }
    }
    return next;
  }

  private token(value: string, prop: string): string | undefined {
    const t = this.hexToToken.get(value.trim().toLowerCase());
    if (!t) {
      this.problems.push(`${prop} "${value}" is not a brand color token; use one of ${Object.keys(this.ctx.brand.tokens).join(", ")}`);
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
      this.problems.push(`"${text.trim().slice(0, 30)}" has a number outside the mono family; wrap copy in <Text> so figures are set in ${mono}`);
    }
    if (inh.color && inh.bg) {
      const key = `${inh.color}/${inh.bg}`;
      if (!this.pairs.has(key)) {
        this.problems.push(`${inh.color} text on ${inh.bg} is not an approved pairing ("${text.trim().slice(0, 30)}")`);
      } else {
        const min = this.pairMin.get(key);
        if (min && (inh.fontSize ?? 16) < min) this.problems.push(`${inh.color} on ${inh.bg} needs ${min}px or larger type ("${text.trim().slice(0, 30)}")`);
      }
    }
    const r = checkText(`"${text.trim().slice(0, 30)}"`, text, this.ctx.brand.bannedWords ?? []);
    this.problems.push(...r.problems);
    this.warnings.push(...r.warnings);
  }

  finish() {
    const rules = this.ctx.brand.accentRules;
    const used = [...this.accentCounts.entries()].filter(([, n]) => n > 0);
    for (const [name, n] of used) {
      if (n > rules.maxElements) this.problems.push(`${name} appears on ${n} elements; the brand allows ${rules.maxElements}`);
    }
    if (!rules.together && used.length > 1) {
      this.problems.push(`${used.map(([n]) => n).join(" and ")} are both used; the brand allows one accent per composition`);
    }
  }
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
