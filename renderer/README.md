# Poster renderer

The Node service that draws Kit's event posters. Kit (Go) starts it as a
child process from `POSTER_RENDERER_DIR` and talks to it over HTTP on
localhost; it keeps no records, so every request carries the brand, the
source and the image references it needs.

Pipeline for one render:

1. **Compile** the poster's TSX with esbuild (transform only). Imports other
   than `react` and `@poster/kit` are refused.
2. **Run** it in a V8 isolate (`isolated-vm`, 64MB, 2s, no host objects).
   `react` is a tiny `createElement` shim; `@poster/kit` is `src/kit/`,
   bundled to CJS at build time and evaluated inside the isolate. The only
   output is the element tree as JSON.
3. **Validate** the tree (`src/validate.ts`): allowed elements only, every
   color a brand token, approved text pairings, one accent element, numbers
   in the mono face, no gradients/shadows/opacity/transforms, copy rules.
4. **Render** with Satori then resvg (`src/render.ts`), and fail any text or
   logo outside the canvas's safe margins.

Images are references (`src/images.ts`): Drive file ids, Pixabay ids with a
download URL, or inline bytes. The host fetches and caches prepared copies
on disk; source code never sees bytes or URLs. Fonts come from Google Fonts
as static TTFs (`src/fonts.ts`) or from `POSTER_FONT_DIR`.

Endpoints: `POST /render`, `/options`, `/template/check`, `/copy/check`,
`/brand/check`, `/inspect`, `/photo`, `/sheet`; `GET /health`. The wire
types are `src/types.ts` and mirror `internal/posterrender/types.go`.

```bash
npm install && npm run build   # dist/server.js, dist/kit.cjs, dist/react.cjs
npm test                        # offline: local fonts in test/fonts, generated photos
KEEP_RENDERS=1 npm test         # also writes PNGs to test/out/
```

The built-in templates live in `internal/apps/posters/builtins/*.tsx` (Kit
embeds them); the tests here render every one at portrait, story and screen.
