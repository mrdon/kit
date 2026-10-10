// The pipeline behind every endpoint: compile -> run in the isolate ->
// validate the tree -> Satori -> safe-area check. One render at a time: the
// memory peak is what sizes the container, not throughput.
import sharp from "sharp";
import { checkContent } from "./copy.ts";
import { loadFonts, type SatoriFont } from "./fonts.ts";
import { ImageStore } from "./images.ts";
import { inlinePoster } from "./inline.ts";
import { photoPool, plan, type PlannedTemplate } from "./options.ts";
import { logoAspects, renderTree, resolveLogos, type ImageSet } from "./render.ts";
import { checkMeta, compile, run, SourceError } from "./sandbox.ts";
import { renderSheet, SHEET_MAX, type SheetItem } from "./sheet.ts";
import type {
  Brand,
  Content,
  Format,
  ImageRef,
  OptionsRequest,
  OptionsResponse,
  PhotoUse,
  RenderRequest,
  RenderResponse,
  TemplateCheckResponse,
  TemplateMeta,
  TemplateProps,
} from "./types.ts";
import { summarizeTree, validateTree } from "./validate.ts";

export class RequestError extends Error {}

export type ServiceOptions = { cacheDir: string; fontDir?: string; driveKey?: string };

export class Service {
  readonly store: ImageStore;
  private queue: Promise<unknown> = Promise.resolve();

  constructor(private opts: ServiceOptions) {
    this.store = new ImageStore(opts.cacheDir, opts.driveKey);
  }

  // Serialises the heavy work. Fetches and validation run inside too: they
  // are cheap next to a render and ordering keeps the memory story simple.
  private serial<T>(fn: () => Promise<T>): Promise<T> {
    const next = this.queue.then(fn, fn);
    this.queue = next.catch(() => undefined);
    return next;
  }

  fonts(brand: Brand): Promise<SatoriFont[]> {
    return loadFonts(brand, this.opts.cacheDir, this.opts.fontDir);
  }

  format(brand: Brand, name: string): Format {
    const f = brand.formats[name];
    if (!f) throw new RequestError(`unknown format "${name}"; the brand defines ${Object.keys(brand.formats).join(", ")}`);
    return f;
  }

  render(req: RenderRequest): Promise<RenderResponse> {
    return this.serial(async () => {
      const format = this.format(req.brand, req.format);
      const fonts = await this.fonts(req.brand);
      const { logos, warnings: logoWarnings } = await resolveLogos(req.brand, this.store);
      const props: TemplateProps | { format: Format } =
        req.kind === "template" ? { ...(req.props as TemplateProps), format } : { format };
      const compiled = compile(req.source);
      const out = run(compiled, props, req.brand, logoAspects(logos));
      const images: ImageSet = { photos: new Map(req.photos.map((p) => [p.id, p])), logos };
      const problems: string[] = [];
      const warnings = [...logoWarnings];
      let meta: TemplateMeta | undefined;
      if (req.kind === "template") {
        problems.push(...checkMeta(out.meta));
        meta = out.meta ?? undefined;
      }
      const v = validateTree(out.tree, { brand: req.brand, photos: images.photos, logoVariants: new Set(logos.keys()) });
      problems.push(...v.problems);
      warnings.push(...v.warnings);
      let png: Buffer = Buffer.alloc(0);
      if (!problems.length) {
        const r = await renderTree(out.tree, format, fonts, images, this.store);
        png = r.png;
        problems.push(...r.problems);
      }
      return {
        png: png.toString("base64"),
        width: format.width,
        height: format.height,
        problems,
        warnings: [...new Set(warnings)],
        meta,
        tree: summarizeTree(out.tree),
      };
    });
  }

  // Up to count options that pass validation and the safe-area check.
  options(req: OptionsRequest): Promise<OptionsResponse> {
    return this.serial(async () => {
      const format = this.format(req.brand, req.format);
      const fonts = await this.fonts(req.brand);
      const { logos } = await resolveLogos(req.brand, this.store);
      const aspects = logoAspects(logos);
      const copy = checkContent(req.content, req.brand.bannedWords);
      if (copy.problems.length) throw new RequestError(`the copy fails the checks: ${copy.problems.join("; ")}`);
      const focus = new Map<string, PhotoUse>();
      if (req.hero) focus.set(req.hero.id, req.hero);
      const pool = photoPool(req.photos, focus, req.hero);
      const grounds = Object.keys(req.brand.grounds);
      const accent = Object.keys(req.brand.accents)[0] ?? "";
      const candidates = plan({ content: req.content, templates: req.templates, grounds, accent, pool, exclude: req.exclude });
      const images: ImageSet = { photos: new Map(req.photos.map((p) => [p.id, p])), logos };
      const out: OptionsResponse = { options: [], skipped: [] };
      for (const c of candidates) {
        if (out.options.length >= req.count) break;
        try {
          const source = inlinePoster(c.template.source, req.content, c.photos, c.ground, c.accent);
          const tree = run(compile(source), { format }, req.brand, aspects).tree;
          const v = validateTree(tree, { brand: req.brand, photos: images.photos, logoVariants: new Set(logos.keys()) });
          if (v.problems.length) {
            out.skipped.push({ templateId: c.template.id, ground: c.ground, reason: v.problems[0] });
            continue;
          }
          const r = await renderTree(tree, format, fonts, images, this.store);
          if (r.problems.length) {
            out.skipped.push({ templateId: c.template.id, ground: c.ground, reason: r.problems[0] });
            continue;
          }
          out.options.push({
            templateId: c.template.id,
            ground: c.ground,
            accent: c.accent,
            photos: c.photos,
            source,
            png: r.png.toString("base64"),
            problems: [],
            warnings: v.warnings,
          });
        } catch (e) {
          const reason = e instanceof Error ? e.message : String(e);
          out.skipped.push({ templateId: c.template.id, ground: c.ground, reason });
        }
      }
      return out;
    });
  }

  // Validates a template on three canvases with sample content, the way
  // activation requires. Photos default to a flat placeholder so a template
  // can be checked before the library has anything in it.
  templateCheck(source: string, brand: Brand, photos: ImageRef[]): Promise<TemplateCheckResponse> {
    return this.serial(async () => {
      const fonts = await this.fonts(brand);
      const { logos } = await resolveLogos(brand, this.store);
      const aspects = logoAspects(logos);
      const compiled = compile(source);
      const refs = photos.length ? photos : [await this.placeholderPhoto(brand)];
      const images: ImageSet = { photos: new Map(refs.map((p) => [p.id, p])), logos };
      const problems: Record<string, string[]> = {};
      const renders: Record<string, string> = {};
      let meta: TemplateMeta | null = null;
      for (const name of checkFormats(brand)) {
        const format = brand.formats[name];
        for (const [label, content] of Object.entries(SAMPLES)) {
          const key = `${name}:${label}`;
          try {
            const uses = refs.slice(0, 3).map((r) => ({ id: r.id, focusX: 0.5, focusY: 0.5, zoom: 1 }));
            const ground = Object.keys(brand.grounds)[0] ?? "paper";
            const accent = Object.keys(brand.accents)[0] ?? "";
            const out = run(compiled, { content, format, photos: uses, ground, accent }, brand, aspects);
            meta = out.meta;
            const metaProblems = checkMeta(out.meta);
            const v = validateTree(out.tree, { brand, photos: images.photos, logoVariants: new Set(logos.keys()) });
            const all = [...metaProblems, ...v.problems];
            if (!all.length) {
              const r = await renderTree(out.tree, format, fonts, images, this.store);
              all.push(...r.problems);
              if (label === "plain") renders[name] = r.png.toString("base64");
            }
            if (all.length) problems[key] = all;
          } catch (e) {
            problems[key] = [e instanceof Error ? e.message : String(e)];
          }
        }
      }
      return { meta, problems, renders };
    });
  }

  private placeholderCache?: ImageRef;
  private async placeholderPhoto(brand: Brand): Promise<ImageRef> {
    if (this.placeholderCache) return this.placeholderCache;
    const fill = Object.values(brand.tokens)[0] ?? "#888888";
    const buf = await sharp({ create: { width: 1600, height: 1200, channels: 3, background: fill } }).jpeg().toBuffer();
    this.placeholderCache = { id: "sample-photo", source: "inline", data: buf.toString("base64"), modified: "1" };
    return this.placeholderCache;
  }

  sheet(items: SheetItem[], brand: Brand): Promise<Buffer> {
    if (items.length > SHEET_MAX) throw new RequestError(`a sheet holds at most ${SHEET_MAX} photos`);
    return this.serial(async () => {
      const fonts = await this.fonts(brand);
      const mono = fonts.find((f) => f.name === brand.fonts.mono.family) ?? fonts[0];
      const grounds = Object.values(brand.grounds);
      const g = grounds[0];
      const bg = g ? brand.tokens[g.rule] ?? brand.tokens[g.bg] : "#dddddd";
      const fg = g ? brand.tokens[g.text] : "#111111";
      return renderSheet(items, this.store, mono, bg, fg);
    });
  }

  copyCheck(content: Content, brand?: Brand) {
    return checkContent(content, brand?.bannedWords);
  }
}

// Which canvases a template must work on: the portrait one, a story and a
// screen if the brand defines them, else whatever it has.
function checkFormats(brand: Brand): string[] {
  const names = Object.keys(brand.formats);
  const pick = (test: (n: string) => boolean) => names.find(test);
  const chosen = [brand.portraitFormat, pick((n) => /story/i.test(n)), pick((n) => /screen|tv/i.test(n))].filter(
    (n): n is string => Boolean(n) && names.includes(n as string),
  );
  return chosen.length ? [...new Set(chosen)] : names.slice(0, 3);
}

// Sample content: an ordinary event and a long one that stresses the title
// and summary boxes.
const SAMPLES: Record<string, Content> = {
  plain: {
    eyebrow: "Every Wednesday",
    title: "Trivia night",
    summary: "Free to play, no sign-up. Turn up with a team, or come on your own and join one.",
    details: [{ label: "When", value: "6:30 to 8:30pm" }],
    action: "Teams of up to 6",
  },
  long: {
    eyebrow: "Sat Nov 14",
    title: "An anniversary party with a very long name indeed",
    summary: "Twelve years in the same room. Live music from 7, the whole tap list on, and a cake that one person is going to regret.",
    details: [
      { label: "When", value: "3 to 10pm" },
      { label: "Where", value: "The back room and the garden" },
    ],
    action: "No tickets, just turn up early",
  },
};

export { SourceError };
