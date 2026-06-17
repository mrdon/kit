import path from 'node:path';
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// Kit serves built assets at /app/assets/* from Go (the shared bundle),
// and the PWA entry HTML at /{slug}/* (per-workspace, resolved from the
// Slack workspace domain). `base: '/app/'` ensures Vite emits absolute
// asset URLs that work under any workspace prefix.
//
// For local dev under `vite dev`, we can't match every workspace path
// generically (Vite's proxy rules don't support path params). The
// gravity-brewing slug is hardcoded here; other workspaces should test
// against the Go backend directly at :8488.
const DEV_SLUG = '/gravity-brewing';

export default defineConfig({
  plugins: [react()],
  base: '/app/',
  // The chat widget is the shared @kit/chat workspace package
  // (web/shared). Alias it to source so Vite's react plugin transforms it
  // as first-party code; deps resolve from the hoisted web/node_modules.
  resolve: {
    alias: { '@chat': path.resolve(__dirname, '../shared/src/chat') },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    // Inline assets so the go:embed tree stays shallow.
    assetsInlineLimit: 0,
  },
  server: {
    port: 5173,
    proxy: {
      [`${DEV_SLUG}/api`]: 'http://localhost:8488',
      [`${DEV_SLUG}/login`]: 'http://localhost:8488',
      [`${DEV_SLUG}/dev-login`]: 'http://localhost:8488',
      [`${DEV_SLUG}/manifest.webmanifest`]: 'http://localhost:8488',
      [`${DEV_SLUG}/icon.svg`]: 'http://localhost:8488',
      [`${DEV_SLUG}/icon-192.png`]: 'http://localhost:8488',
      [`${DEV_SLUG}/icon-512.png`]: 'http://localhost:8488',
      [`${DEV_SLUG}/sw.js`]: 'http://localhost:8488',
      '/oauth/callback': 'http://localhost:8488',
    },
  },
});
