// Photos and logos. Source code never sees bytes or URLs: Kit sends image
// references, this module fetches them (Drive, Pixabay, a URL, or inline
// bytes), keeps a prepared copy on disk, and crops to a layout's box.
import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import sharp from "sharp";
import { c2paVerdict, type Verdict } from "./c2pa.ts";
import type { Format, ImageRef, InspectResponse, PhotoUse } from "./types.ts";

// Working size: the largest canvas is 2400px, and camera originals make
// every render slow for no visible gain.
const MAX_EDGE = 2400;
const CACHE_LIMIT_BYTES = Number(process.env.POSTER_IMAGE_CACHE_MB ?? 2048) * 1024 * 1024;

sharp.cache(false);
sharp.concurrency(1);

export class ImageError extends Error {}

export type Prepared = {
  file: string;
  width: number;
  height: number;
  c2pa: Verdict;
  // The source had transparency, so the prepared copy is a PNG and crops
  // of it stay transparent. A logo dropped in as a photo would otherwise
  // get a black box where its background was.
  alpha: boolean;
};

export class ImageStore {
  private dir: string;
  private inflight = new Map<string, Promise<Prepared>>();

  constructor(cacheDir: string, private driveKey?: string) {
    this.dir = path.join(cacheDir, "images");
    fs.mkdirSync(this.dir, { recursive: true });
  }

  private key(ref: ImageRef) {
    return crypto.createHash("sha1").update(`${ref.source}|${ref.id}|${ref.fileId ?? ""}|${ref.modified ?? ""}|${ref.url ?? ""}|${ref.thumbUrl ?? ""}`).digest("hex");
  }

  // An upright JPEG no larger than the working size, plus what the sync
  // needs to know about it. Fetched once per (id, modified).
  prepare(ref: ImageRef): Promise<Prepared> {
    const key = this.key(ref);
    let p = this.inflight.get(key);
    if (!p) {
      p = this.prepareUncached(ref, key).finally(() => this.inflight.delete(key));
      this.inflight.set(key, p);
    }
    return p;
  }

  private async prepareUncached(ref: ImageRef, key: string): Promise<Prepared> {
    const metaFile = path.join(this.dir, `${key}.json`);
    if (fs.existsSync(metaFile)) {
      const meta = JSON.parse(fs.readFileSync(metaFile, "utf8")) as Omit<Prepared, "file"> & { ext?: string };
      const file = path.join(this.dir, `${key}.${meta.ext ?? "jpg"}`);
      if (fs.existsSync(file)) {
        const now = new Date();
        fs.utimesSync(file, now, now);
        return { file, width: meta.width, height: meta.height, c2pa: meta.c2pa, alpha: meta.alpha ?? false };
      }
    }
    const raw = await this.fetch(ref);
    const c2pa = c2paVerdict(raw);
    let info: sharp.OutputInfo;
    let alpha = false;
    let file = "";
    try {
      alpha = Boolean((await sharp(raw).metadata()).hasAlpha);
      file = path.join(this.dir, `${key}.${alpha ? "png" : "jpg"}`);
      const resized = sharp(raw).rotate().resize(MAX_EDGE, MAX_EDGE, { fit: "inside", withoutEnlargement: true });
      info = await (alpha ? resized.png() : resized.jpeg({ quality: 92 })).toFile(file);
    } catch (e) {
      throw new ImageError(`image ${ref.id} could not be decoded: ${e instanceof Error ? e.message : String(e)}`);
    }
    const meta = { width: info.width, height: info.height, c2pa, alpha, ext: alpha ? "png" : "jpg" };
    fs.writeFileSync(metaFile, JSON.stringify(meta));
    this.prune();
    return { file, width: info.width, height: info.height, c2pa, alpha };
  }

  private async fetch(ref: ImageRef): Promise<Buffer> {
    if (ref.source === "inline") {
      if (!ref.data) throw new ImageError(`image ${ref.id} has no data`);
      return Buffer.from(ref.data, "base64");
    }
    if (ref.thumbUrl) {
      // Drive refuses some thumbnails; the original is the fallback.
      const small = await this.get(ref.thumbUrl);
      if (small) return small;
    }
    const buf = await this.get(ref.url ?? this.driveURL(ref));
    if (!buf) {
      throw new ImageError(`image ${ref.id} could not be downloaded; for Drive, is the folder shared with anyone who has the link?`);
    }
    return buf;
  }

  // One GET; null when the answer is not an image.
  private async get(url: string): Promise<Buffer | null> {
    const res = await fetch(url, { redirect: "follow" });
    const type = res.headers.get("content-type") ?? "";
    if (!res.ok || type.startsWith("text/html")) return null;
    return Buffer.from(await res.arrayBuffer());
  }

  private driveURL(ref: ImageRef) {
    if (ref.source !== "drive") throw new ImageError(`image ${ref.id} has no url`);
    const fileId = ref.fileId ?? ref.id;
    if (this.driveKey) return `https://www.googleapis.com/drive/v3/files/${encodeURIComponent(fileId)}?alt=media&key=${this.driveKey}`;
    return `https://drive.usercontent.google.com/download?id=${encodeURIComponent(fileId)}&export=download&confirm=t`;
  }

  // Oldest-touched files go first once the cache passes its size limit.
  private prune() {
    const entries = fs
      .readdirSync(this.dir)
      .filter((f) => f.endsWith(".jpg"))
      .map((f) => {
        const st = fs.statSync(path.join(this.dir, f));
        return { f, size: st.size, at: st.mtimeMs };
      });
    let total = entries.reduce((n, e) => n + e.size, 0);
    if (total <= CACHE_LIMIT_BYTES) return;
    entries.sort((a, b) => a.at - b.at);
    for (const e of entries) {
      if (total <= CACHE_LIMIT_BYTES * 0.8) break;
      fs.rmSync(path.join(this.dir, e.f), { force: true });
      fs.rmSync(path.join(this.dir, e.f.replace(/\.jpg$/, ".json")), { force: true });
      total -= e.size;
    }
  }

  async inspect(ref: ImageRef): Promise<InspectResponse> {
    const p = await this.prepare(ref);
    const thumb = await sharp(p.file).resize(512, 512, { fit: "inside" }).jpeg({ quality: 80 }).toBuffer();
    const orientation = p.width > p.height * 1.1 ? "landscape" : p.height > p.width * 1.1 ? "portrait" : "square";
    return { width: p.width, height: p.height, orientation, c2pa: p.c2pa, thumbnail: thumb.toString("base64") };
  }

  // One photo at a size, for a model to look at.
  async photo(ref: ImageRef, size: number): Promise<Buffer> {
    const p = await this.prepare(ref);
    return sharp(p.file).resize(size, size, { fit: "inside", withoutEnlargement: true }).jpeg({ quality: 85 }).toBuffer();
  }

  // A logo trimmed of its padding, as a PNG data URI with its box.
  async logo(ref: ImageRef): Promise<{ uri: string; width: number; height: number }> {
    const key = `logo-${this.key(ref)}`;
    const file = path.join(this.dir, `${key}.png`);
    if (!fs.existsSync(file)) {
      const raw = await this.fetch(ref);
      try {
        await sharp(raw).trim().png().toFile(file);
      } catch (e) {
        throw new ImageError(`logo ${ref.id} could not be decoded: ${e instanceof Error ? e.message : String(e)}`);
      }
    }
    const meta = await sharp(file).metadata();
    const buf = fs.readFileSync(file);
    return { uri: `data:image/png;base64,${buf.toString("base64")}`, width: meta.width ?? 1, height: meta.height ?? 1 };
  }
}

export type Fitted = { uri: string; focusX: number; focusY: number };

// Upright, cut to the zoom around the focus point, and no bigger than it
// takes to cover the canvas. The layout fits it to its box with object-fit
// and object-position, using the focus point moved into the crop.
export async function fitPhoto(prepared: Prepared, use: PhotoUse, format: Format): Promise<Fitted> {
  const { width: w, height: h } = prepared;
  const zoom = Math.min(3, Math.max(1, use.zoom || 1));
  const cw = Math.round(w / zoom);
  const ch = Math.round(h / zoom);
  const left = Math.min(w - cw, Math.max(0, Math.round(use.focusX * w - cw / 2)));
  const top = Math.min(h - ch, Math.max(0, Math.round(use.focusY * h - ch / 2)));
  const cropped = sharp(prepared.file)
    .extract({ left, top, width: cw, height: ch })
    .resize(format.width, format.height, { fit: "outside", withoutEnlargement: true, kernel: "lanczos3" });
  const buf = await (prepared.alpha ? cropped.png() : cropped.jpeg({ quality: 92 })).toBuffer();
  const pos = (f: number, size: number, start: number, cut: number) =>
    cut === size ? f : Math.min(1, Math.max(0, (f * size - start) / cut));
  return {
    uri: `data:image/${prepared.alpha ? "png" : "jpeg"};base64,${buf.toString("base64")}`,
    focusX: zoom === 1 ? use.focusX : pos(use.focusX, w, left, cw),
    focusY: zoom === 1 ? use.focusY : pos(use.focusY, h, top, ch),
  };
}
