// Element tree -> Satori (SVG, text as paths) -> resvg (PNG), then the
// safe-area check over the node boxes Satori reports.
import { Resvg } from "@resvg/resvg-js";
import satori from "satori";
import type { SatoriFont } from "./fonts.ts";
import { fitPhoto, type Fitted, type ImageStore } from "./images.ts";
import type { Brand, Format, ImageRef, PhotoUse, TreeNode } from "./types.ts";

type SatoriElement = { type: string; key?: string; props: Record<string, unknown> };
type SatoriNode = SatoriElement | string;

export type ResolvedLogo = { uri: string; width: number; height: number };

export type ImageSet = {
  photos: Map<string, ImageRef>;
  logos: Map<string, ResolvedLogo>;
};

// Resolves the logos a brand maps, tolerating ones that cannot be fetched
// (their space is left empty and the render carries a warning).
export async function resolveLogos(brand: Brand, store: ImageStore): Promise<{ logos: Map<string, ResolvedLogo>; warnings: string[] }> {
  const logos = new Map<string, ResolvedLogo>();
  const warnings: string[] = [];
  for (const [variant, ref] of Object.entries(brand.logos ?? {})) {
    try {
      logos.set(variant, await store.logo(ref));
    } catch (e) {
      warnings.push(`the ${variant} logo could not be loaded: ${e instanceof Error ? e.message : String(e)}`);
    }
  }
  return { logos, warnings };
}

export function logoAspects(logos: Map<string, ResolvedLogo>): Record<string, number> {
  return Object.fromEntries([...logos].map(([v, l]) => [v, l.width / l.height]));
}

export type Rendered = { png: Buffer; problems: string[] };

export async function renderTree(
  tree: TreeNode,
  format: Format,
  fonts: SatoriFont[],
  images: ImageSet,
  store: ImageStore,
): Promise<Rendered> {
  const fitted = new Map<string, Fitted>();
  await collectPhotos(tree, format, images, store, fitted);
  const element = toSatori(tree, fitted, images.logos);
  const nodes: DetectedNode[] = [];
  const svg = await satori(element as never, {
    width: format.width,
    height: format.height,
    fonts,
    onNodeDetected: (n) => nodes.push(n as DetectedNode),
  });
  const png = new Resvg(svg, { font: { loadSystemFonts: false } }).render().asPng();
  return { png: Buffer.from(png), problems: safeAreaProblems(nodes, format) };
}

const useKey = (u: PhotoUse) => `${u.id}|${u.focusX}|${u.focusY}|${u.zoom}`;

async function collectPhotos(node: TreeNode, format: Format, images: ImageSet, store: ImageStore, out: Map<string, Fitted>) {
  if (node === null || typeof node !== "object") return;
  if (node.type === "__photo") {
    const use = photoUse(node.props);
    const key = useKey(use);
    if (!out.has(key)) {
      const ref = images.photos.get(use.id);
      if (!ref) return; // validation already reported it
      out.set(key, await fitPhoto(await store.prepare(ref), use, format));
    }
    return;
  }
  for (const c of node.children) await collectPhotos(c, format, images, store, out);
}

function photoUse(props: Record<string, unknown>): PhotoUse {
  const num = (v: unknown, d: number) => (typeof v === "number" && Number.isFinite(v) ? v : d);
  return {
    id: String(props.id ?? ""),
    focusX: Math.min(1, Math.max(0, num(props.focusX, 0.5))),
    focusY: Math.min(1, Math.max(0, num(props.focusY, 0.5))),
    zoom: Math.min(3, Math.max(1, num(props.zoom, 1))),
  };
}

// Builds the React-shaped object tree Satori walks. Fragments flatten into
// their parent; photo and logo markers become <img> with resolved bytes.
function toSatori(node: TreeNode, fitted: Map<string, Fitted>, logos: Map<string, ResolvedLogo>): SatoriNode | null {
  if (node === null) return null;
  if (typeof node === "string" || typeof node === "number") return String(node);
  if (node.type === "__photo") {
    const f = fitted.get(useKey(photoUse(node.props)));
    if (!f) return null;
    return {
      type: "img",
      props: {
        src: f.uri,
        style: { width: "100%", height: "100%", objectFit: "cover", objectPosition: `${f.focusX * 100}% ${f.focusY * 100}%` },
      },
    };
  }
  if (node.type === "__logo") {
    const logo = logos.get(String(node.props.variant));
    const width = Number(node.props.width);
    const height = Number(node.props.height);
    if (!logo) return { type: "div", props: { style: { width, height } } };
    return { type: "img", key: "logo", props: { src: logo.uri, width, height, style: { width, height } } };
  }
  const children = flattenChildren(node.children, fitted, logos);
  const props: Record<string, unknown> = { ...node.props };
  if (children.length === 1 && typeof children[0] === "string") props.children = children[0];
  else if (children.length) props.children = children;
  const el: SatoriElement = { type: node.type, props };
  if (node.key !== undefined) el.key = node.key;
  return el;
}

function flattenChildren(children: TreeNode[], fitted: Map<string, Fitted>, logos: Map<string, ResolvedLogo>): SatoriNode[] {
  const out: SatoriNode[] = [];
  for (const c of children) {
    if (c !== null && typeof c === "object" && c.type === "__fragment") {
      out.push(...flattenChildren(c.children, fitted, logos));
      continue;
    }
    const s = toSatori(c, fitted, logos);
    if (s !== null) out.push(s);
  }
  return out;
}

type DetectedNode = { key: string | null; left: number; top: number; width: number; height: number; textContent?: string };

// Copy and the logo must sit inside the canvas's safe margins; photos may
// bleed. This is how a too-long title gets caught without a model.
function safeAreaProblems(nodes: DetectedNode[], format: Format): string[] {
  const { width, height, safe } = format;
  const slack = 2;
  const problems: string[] = [];
  for (const n of nodes) {
    const what = n.key === "logo" ? "the logo" : n.textContent?.trim() ? `"${n.textContent.trim().slice(0, 30)}"` : null;
    if (!what) continue;
    const out =
      n.left < safe.left - slack ||
      n.top < safe.top - slack ||
      n.left + n.width > width - safe.right + slack ||
      n.top + n.height > height - safe.bottom + slack;
    if (out) problems.push(`${what} is outside the safe area`);
  }
  return problems;
}
