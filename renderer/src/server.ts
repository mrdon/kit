// The HTTP face of the renderer. Kit talks to it on localhost (or wherever
// POSTER_RENDERER_URL points) with a bearer token. It keeps no records.
import http from "node:http";
import os from "node:os";
import path from "node:path";
import { FontError } from "./fonts.ts";
import { ImageError } from "./images.ts";
import { InlineError } from "./inline.ts";
import { SourceError } from "./sandbox.ts";
import { RequestError, Service } from "./service.ts";
import type { Brand, Content, ImageRef, OptionsRequest, RenderRequest } from "./types.ts";

const PORT = Number(process.env.POSTER_RENDERER_PORT ?? 8491);
const HOST = process.env.POSTER_RENDERER_HOST ?? "127.0.0.1";
const TOKEN = process.env.POSTER_RENDERER_TOKEN ?? "";
const MAX_BODY = 48 << 20;

export const service = new Service({
  cacheDir: process.env.POSTER_RENDERER_CACHE_DIR ?? path.join(os.tmpdir(), "kit-poster-renderer"),
  fontDir: process.env.POSTER_FONT_DIR || undefined,
  driveKey: process.env.GDRIVE_API_KEY || undefined,
});

type Handler = (body: Record<string, unknown>) => Promise<{ status?: number; body: unknown }>;

const routes: Record<string, Handler> = {
  "/render": async (b) => ({ body: await service.render(b as unknown as RenderRequest) }),
  "/options": async (b) => ({ body: await service.options(b as unknown as OptionsRequest) }),
  "/inspect": async (b) => ({ body: await service.store.inspect(b.image as ImageRef) }),
  "/photo": async (b) => {
    const size = Math.min(2048, Math.max(64, Number(b.size ?? 1024)));
    const jpeg = await service.store.photo(b.image as ImageRef, size);
    return { body: { jpeg: jpeg.toString("base64") } };
  },
  "/sheet": async (b) => {
    const png = await service.sheet(b.items as never, b.brand as Brand);
    return { body: { png: png.toString("base64") } };
  },
  "/template/check": async (b) => ({
    body: await service.templateCheck(String(b.source ?? ""), b.brand as Brand, (b.photos as ImageRef[]) ?? []),
  }),
  "/copy/check": async (b) => ({ body: service.copyCheck(b.content as Content, b.brand as Brand | undefined) }),
  "/brand/check": async (b) => {
    const { checkFonts } = await import("./fonts.ts");
    const problems = await checkFonts(b.brand as Brand, service["opts"].cacheDir, service["opts"].fontDir);
    return { body: { problems } };
  },
};

function statusFor(e: unknown): number {
  if (e instanceof RequestError || e instanceof SourceError || e instanceof InlineError) return 400;
  if (e instanceof ImageError || e instanceof FontError) return 422;
  return 500;
}

function readBody(req: http.IncomingMessage): Promise<Record<string, unknown>> {
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = [];
    let size = 0;
    req.on("data", (c: Buffer) => {
      size += c.length;
      if (size > MAX_BODY) {
        reject(new RequestError("request body too large"));
        req.destroy();
        return;
      }
      chunks.push(c);
    });
    req.on("end", () => {
      try {
        resolve(chunks.length ? (JSON.parse(Buffer.concat(chunks).toString("utf8")) as Record<string, unknown>) : {});
      } catch {
        reject(new RequestError("request body is not JSON"));
      }
    });
    req.on("error", reject);
  });
}

function send(res: http.ServerResponse, status: number, body: unknown) {
  const json = JSON.stringify(body);
  res.writeHead(status, { "Content-Type": "application/json", "Content-Length": Buffer.byteLength(json) });
  res.end(json);
}

export const server = http.createServer(async (req, res) => {
  const url = req.url ?? "/";
  if (req.method === "GET" && url === "/health") {
    send(res, 200, { ok: true, pid: process.pid, rss_mb: Math.round(process.memoryUsage().rss / 1048576) });
    return;
  }
  if (TOKEN && req.headers.authorization !== `Bearer ${TOKEN}`) {
    send(res, 401, { error: "unauthorized" });
    return;
  }
  const handler = req.method === "POST" ? routes[url] : undefined;
  if (!handler) {
    send(res, 404, { error: "not found" });
    return;
  }
  const started = Date.now();
  try {
    const body = await readBody(req);
    const out = await handler(body);
    send(res, out.status ?? 200, out.body);
  } catch (e) {
    const status = statusFor(e);
    const message = e instanceof Error ? e.message : String(e);
    if (status === 500) console.error(`renderer: ${url} failed:`, e);
    send(res, status, { error: message });
  } finally {
    console.log(`renderer: ${url} ${Date.now() - started}ms rss=${Math.round(process.memoryUsage().rss / 1048576)}MB`);
  }
});

if (process.env.POSTER_RENDERER_NO_LISTEN !== "1") {
  server.listen(PORT, HOST, () => {
    console.log(`renderer: listening on http://${HOST}:${PORT}`);
  });
  const stop = () => {
    server.close(() => process.exit(0));
    setTimeout(() => process.exit(0), 2000).unref();
  };
  process.on("SIGTERM", stop);
  process.on("SIGINT", stop);
  // Kit holds the other end of stdin; when it goes away, so do we.
  process.stdin.on("end", stop);
  process.stdin.resume();
}
