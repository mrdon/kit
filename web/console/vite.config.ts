import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// The console is a desktop-oriented React tool served at /{slug}/web/*.
// Its built assets live at /console/assets/* (a stable, shared prefix
// distinct from the cards PWA's /app/assets/*), while the per-workspace
// entry HTML is served at /{slug}/web by Go. `base: '/console/'` makes
// Vite emit absolute asset URLs that resolve under any workspace prefix.
//
// For local dev under `vite dev`, Vite's proxy can't match every
// workspace path generically (no path params). The gravity-brewing slug
// is hardcoded; other workspaces should test against the Go backend at
// :8488 directly.
const DEV_SLUG = '/gravity-brewing';

export default defineConfig({
  plugins: [react()],
  base: '/console/',
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    // Inline nothing so the go:embed tree stays flat and cacheable.
    assetsInlineLimit: 0,
  },
  server: {
    port: 5174,
    proxy: {
      [`${DEV_SLUG}/web/api`]: 'http://localhost:8488',
      [`${DEV_SLUG}/login`]: 'http://localhost:8488',
      [`${DEV_SLUG}/dev-login`]: 'http://localhost:8488',
      [`${DEV_SLUG}/icon.svg`]: 'http://localhost:8488',
      '/oauth/callback': 'http://localhost:8488',
    },
  },
});
