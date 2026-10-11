// --- Posters ---
//
// Mirrors internal/apps/posters JSON. A poster is one event's chain of
// versions; options are versions that share a batch; templates are the
// layouts the generator spreads across. Render URLs are plain <img> sources
// (the session cookie goes along), cache-busted by the version id.

export interface PosterDetailLine {
  label: string;
  value: string;
}

export interface PosterContent {
  eyebrow: string;
  title: string;
  summary?: string;
  details: PosterDetailLine[];
  action?: string;
}

export interface PosterPhotoUse {
  id: string;
  focusX: number;
  focusY: number;
  zoom: number;
}

export interface PosterVersion {
  id: string;
  poster_id: string;
  parent_id?: string;
  batch_id?: string;
  template_id?: string;
  template_version_id?: string;
  source: string;
  content: PosterContent;
  photos: PosterPhotoUse[];
  ground: string;
  format: string;
  problems: string[];
  instruction: string;
  author: PosterAuthor;
  picked: boolean;
  created_at: string;
}

/** A version with its portrait render URL — an option in a batch, or a
 *  history entry on the poster page. */
export interface PosterOption extends PosterVersion {
  thumb: string;
}

export type PosterStatus = 'on the event' | 'draft' | 'out of date';

export interface Poster {
  id: string;
  tenant_id: string;
  event_id?: string;
  title: string;
  current_version_id?: string;
  set_version_id?: string;
  facts?: unknown;
  stale: boolean;
  created_by?: string;
  created_at: string;
  updated_at: string;
  event_title?: string;
  event_slug?: string;
  status: PosterStatus;
  /** The current version's portrait render URL, for lists. */
  thumb?: string;
}

export interface PosterForEvent {
  poster: Poster | null;
  options: PosterOption[];
  renderer_ready: boolean;
  /** False when this server has no renderer at all; ready=false alone means it is starting. */
  renderer_configured: boolean;
  brand_ready: boolean;
  brand_problem?: string;
}

export interface PosterDetail {
  poster: Poster;
  current: PosterVersion | null;
  /** History, newest first. Unpicked options are left out. */
  versions: PosterOption[];
  /** The latest option batch, oldest first. */
  options: PosterOption[];
  formats: string[];
  portrait_format: string;
  renderer_ready: boolean;
}

export interface PosterGenerateResult {
  poster: Poster;
  options: PosterOption[];
  skipped: number;
  /** The index had nothing honest for this event; only type layouts were offered. */
  no_photo: boolean;
  /** A "more options" run rewrote the copy because the event changed. */
  fresh_copy: boolean;
}

/** Mirror of posters.GenerateStage (Go). */
export const PosterGenerateStage = {
  Copy: 'copy',
  Photo: 'photo',
  Rendering: 'rendering',
} as const;
export type PosterGenerateStage = (typeof PosterGenerateStage)[keyof typeof PosterGenerateStage];

/** The one SSE event the generate stream adds to ChatEvent (Go: eventProgress). */
export const PosterGenerateEvent = { Progress: 'progress' } as const;

/** Mirror of posters.Author (Go): who made a version. */
export const PosterAuthor = {
  User: 'user',
  Agent: 'agent',
  System: 'system',
  Builtin: 'builtin',
} as const;
export type PosterAuthor = (typeof PosterAuthor)[keyof typeof PosterAuthor];

/** Mirror of posterrender.C2PA (Go): an image's provenance verdict. */
export const PhotoC2PA = {
  None: 'none',
  Camera: 'camera',
  AI: 'ai',
  Unknown: 'unknown',
} as const;
export type PhotoC2PA = (typeof PhotoC2PA)[keyof typeof PhotoC2PA];

export interface PosterUpdateResult {
  version?: PosterVersion;
  problems: string[];
  warnings?: string[];
  changes: string[] | null;
}

export interface PosterTemplateMeta {
  name: string;
  description: string;
  photos: { min: number; max: number };
  needs: string[] | null;
}

/** Mirror of posters.TemplateStatus (Go): a tenant template's lifecycle. */
export const PosterTemplateStatus = { Draft: 'draft', Active: 'active', Archived: 'archived' } as const;
export type PosterTemplateStatus = (typeof PosterTemplateStatus)[keyof typeof PosterTemplateStatus];

/** Mirror of posters.TemplateOrigin (Go). */
export const PosterTemplateOrigin = {
  Builtin: 'builtin',
  Poster: 'poster',
  Image: 'image',
  Chat: 'chat',
} as const;
export type PosterTemplateOrigin = (typeof PosterTemplateOrigin)[keyof typeof PosterTemplateOrigin];

/** Mirror of posters.BuiltinVisibility (Go): the status words a built-in takes. */
export const PosterBuiltinVisibility = { Hidden: 'hidden', Visible: 'visible' } as const;
export type PosterBuiltinVisibility = (typeof PosterBuiltinVisibility)[keyof typeof PosterBuiltinVisibility];

export interface PosterTemplate {
  id: string;
  /** Absent on a built-in: it ships with Kit and cannot be edited in place. */
  tenant_id?: string;
  builtin_key?: string;
  name: string;
  description: string;
  status: PosterTemplateStatus;
  origin: PosterTemplateOrigin;
  meta: PosterTemplateMeta;
  current_version_id?: string;
  parent_template_id?: string;
  source_poster_id?: string;
  reference_attachment_id?: string;
  created_by?: string;
  created_at: string;
  updated_at: string;
  /** A built-in this workspace chose not to offer. */
  hidden: boolean;
  picks: number;
  last_used?: string;
  /** "Usually edited to…": instructions behind agent edits after a pick. */
  edits?: string[];
}

export interface PosterTemplateVersion {
  id: string;
  template_id: string;
  parent_id?: string;
  source: string;
  summary: string;
  author: PosterAuthor;
  created_by?: string;
  created_at: string;
}

export interface PosterTemplateDetail {
  template: PosterTemplate;
  versions: PosterTemplateVersion[];
  formats: string[];
}

export type PosterPhotoStatus = 'pending' | 'indexed' | 'removed';

export interface PosterPhoto {
  id: string;
  tenant_id: string;
  drive_file_id: string;
  drive_modified_at: string;
  folder: string;
  filename: string;
  width: number;
  height: number;
  orientation: string;
  status: PosterPhotoStatus;
  description: string;
  tags: string[];
  focus_x: number;
  focus_y: number;
  notes: string;
  c2pa: PhotoC2PA;
  indexed_by: string;
  indexed_at?: string;
  created_at: string;
  updated_at: string;
  /** Drive's thumbnail; small and sometimes refused, so fall back to the image endpoint. */
  thumb: string;
}

export interface PosterPhotoCounts {
  pending: number;
  indexed: number;
  removed: number;
}

export interface PosterPhotosPayload {
  photos: PosterPhoto[];
  folders: string[];
  counts: PosterPhotoCounts;
}

export interface PosterPhotoPatch {
  description?: string;
  tags?: string[];
  focus_x?: number;
  focus_y?: number;
  notes?: string;
}

// The derived brand, as posterrender.Brand serialises it (camelCase: it is
// the renderer's wire format, not the console's).
export interface PosterBrandGround {
  bg: string;
  text: string;
  label: string;
  rule: string;
  logo: string;
}

export interface PosterBrandAccent {
  fill: string;
  text: string;
}

export interface PosterBrandPair {
  fg: string;
  bg: string;
  minSizePx?: number;
}

export interface PosterBrandFontRole {
  family: string;
  weights: number[] | null;
  advance?: number;
}

export interface PosterBrandFormat {
  width: number;
  height: number;
  safe: { top: number; right: number; bottom: number; left: number };
}

export interface PosterBrand {
  hash: string;
  tokens: Record<string, string> | null;
  grounds: Record<string, PosterBrandGround> | null;
  accents: Record<string, PosterBrandAccent> | null;
  accentRules: { maxElements: number; together: boolean };
  pairs: PosterBrandPair[] | null;
  fonts: {
    display: PosterBrandFontRole;
    text: PosterBrandFontRole;
    mono: PosterBrandFontRole;
  };
  formats: Record<string, PosterBrandFormat> | null;
  portraitFormat: string;
  bannedWords?: string[];
}

export interface PosterBrandStatus {
  brand?: PosterBrand;
  hash: string;
  problems: string[] | null;
  /** "json" (parsed from the guide's block), "model" (extracted), or "" when there is no guide. */
  source: string;
  skill_id?: string;
  updated_at?: string;
}

export interface PosterLogoFile {
  file_id: string;
  name: string;
  modified: string;
}

export interface PosterLogoCandidate extends PosterLogoFile {
  thumb: string;
  variant?: string;
}

export interface PostersSettingsRow {
  tenant_id: string;
  photo_folder_id: string;
  logo_folder_id: string;
  logo_map: Record<string, PosterLogoFile>;
  allow_stock_photos: boolean;
  auto_sync: boolean;
  brand_hash: string;
  brand_problems: string[];
  brand_derived_at?: string;
  last_sync_at?: string;
  last_sync_error: string;
  updated_at: string;
}

export interface PostersSettings {
  settings: PostersSettingsRow;
  photo_folder_url: string;
  logo_folder_url: string;
  logo_files: PosterLogoCandidate[];
  logo_variants: string[];
  logo_error?: string;
  brand: PosterBrandStatus | null;
  font_problems: string[];
  counts: PosterPhotoCounts;
  renderer_ready: boolean;
  /** The server has a Pixabay key; without it the stock switch does nothing. */
  stock_configured: boolean;
  /** What to paste into Claude Code to describe the pending photos. */
  index_prompt: string;
}

export interface PostersSettingsBody {
  photo_folder_url: string;
  logo_folder_url: string;
  logo_map: Record<string, PosterLogoFile>;
  allow_stock_photos: boolean;
  auto_sync: boolean;
}

export interface PosterSyncResult {
  listed: number;
  new: number;
  inspected: number;
  removed: number;
  pending: number;
  problems: string[] | null;
}
