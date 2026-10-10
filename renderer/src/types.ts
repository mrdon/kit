// Wire types shared by every endpoint. Kit (Go) builds these; the renderer
// never reads a database, so everything a render needs rides on the request.

export type Format = {
  width: number;
  height: number;
  safe: { top: number; right: number; bottom: number; left: number };
};

export type GroundSpec = {
  // Token names, not hex: the validator reasons in tokens and the isolate's
  // kit resolves them to hex.
  bg: string;
  text: string;
  label: string;
  rule: string;
  logo: string; // logo variant for this ground
};

export type AccentSpec = { fill: string; text: string };

export type FontRole = {
  family: string;
  weights: number[];
  // Average glyph advance as a fraction of the font size, used by fitTitle
  // to pick a size without measuring glyphs. Satori's safe-area check is the
  // backstop when the guess is wrong.
  advance?: number;
};

export type Pair = { fg: string; bg: string; minSizePx?: number };

// Brand is the per-tenant visual system, derived by Kit from the tenant's
// branding-guide skill. Everything here is tokens and names; hex values live
// only in `tokens`.
export type Brand = {
  hash: string;
  tokens: Record<string, string>;
  grounds: Record<string, GroundSpec>;
  accents: Record<string, AccentSpec>;
  accentRules: { maxElements: number; together: boolean };
  pairs: Pair[];
  fonts: { display: FontRole; text: FontRole; mono: FontRole };
  formats: Record<string, Format>;
  portraitFormat: string;
  logos: Record<string, ImageRef>;
  // Extra banned words for the copy check, from the tenant's writing-copy
  // skill. The mechanical rules (dashes, caps, emoji) apply to everyone.
  bannedWords?: string[];
};

export type ImageSource = "drive" | "pixabay" | "url" | "inline";

// An image the host may fetch. Source code never sees one of these: it
// refers to photos by id and the host resolves the id against the request's
// photo list.
export type ImageRef = {
  id: string;
  source: ImageSource;
  url?: string;
  // Cache key discriminator (Drive modifiedTime, Pixabay id...).
  modified?: string;
  // Only for source "inline": base64 bytes.
  data?: string;
  folder?: string;
};

// How a layout uses a photo: which part to keep and how tight.
export type PhotoUse = {
  id: string;
  focusX: number;
  focusY: number;
  zoom: number;
};

export type Detail = { label: string; value: string };

export type Content = {
  eyebrow: string;
  title: string;
  summary?: string;
  details: Detail[];
  action?: string;
};

export type TemplateMeta = {
  name: string;
  description: string;
  photos: { min: number; max: number };
  needs: string[];
};

export type TemplateProps = {
  content: Content;
  format: Format;
  photos: PhotoUse[];
  ground: string;
  accent: string;
};

export type RenderRequest = {
  kind: "poster" | "template";
  source: string;
  format: string;
  brand: Brand;
  photos: ImageRef[];
  // Template renders take the props a poster would have inlined.
  props?: TemplateProps;
};

export type RenderResponse = {
  png: string; // base64
  width: number;
  height: number;
  problems: string[];
  warnings: string[];
  meta?: TemplateMeta;
  // A short structural summary of the tree, for a reply.
  tree?: string;
};

// A template the option generator may use, with the meta Kit stored.
export type PlannedTemplate = { id: string; source: string; weight: number; meta: TemplateMeta };

export type OptionsRequest = {
  content: Content;
  brand: Brand;
  format: string;
  photos: ImageRef[];
  hero?: PhotoUse;
  templates: PlannedTemplate[];
  count: number;
  exclude?: string[];
};

export type OptionResult = {
  templateId: string;
  ground: string;
  accent: string;
  photos: PhotoUse[];
  source: string;
  png: string;
  problems: string[];
  warnings: string[];
};

export type OptionsResponse = {
  options: OptionResult[];
  skipped: Array<{ templateId: string; ground: string; reason: string }>;
};

export type TemplateCheckResponse = {
  meta: TemplateMeta | null;
  // Keyed "format:sample".
  problems: Record<string, string[]>;
  renders: Record<string, string>;
};

export type CopyCheck = { problems: string[]; warnings: string[] };

export type SheetItem = { image: ImageRef; label: string };

export type InspectResponse = {
  width: number;
  height: number;
  orientation: "landscape" | "portrait" | "square";
  c2pa: "none" | "camera" | "ai" | "unknown";
  thumbnail: string; // base64 JPEG, longest edge 512
};

// The element tree the isolate hands back. Function components have
// already run; only host-level types remain.
export type TreeNode =
  | string
  | number
  | null
  | { type: string; key?: string; props: Record<string, unknown>; children: TreeNode[] };
