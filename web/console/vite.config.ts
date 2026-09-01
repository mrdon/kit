import path from 'node:path';
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
  // `@chat` is the shared @kit/chat workspace package (web/shared),
  // aliased to source so Vite's react plugin transforms it as first-party
  // code; deps resolve from the hoisted web/node_modules.
  resolve: {
    alias: { '@chat': path.resolve(__dirname, '../shared/src/chat') },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    // Inline nothing so the go:embed tree stays flat and cacheable.
    assetsInlineLimit: 0,
    // Two entries, one build, one hashed asset prefix:
    //   main — the authenticated console (index.html, served at /{slug}/web)
    //   play — the trivia player (play.html, served at /{slug}/trivia/{game})
    //
    // The player is a separate entry rather than a route inside the console
    // because the two share nothing: the console is authenticated, desktop
    // and dense, while the player is a phone in a dark bar held by somebody
    // with no Kit account, and it must not ship the console's auth code,
    // Shell or launcher.
    rollupOptions: {
      input: {
        main: path.resolve(__dirname, 'index.html'),
        play: path.resolve(__dirname, 'play.html'),
      },
    },
  },
  server: {
    port: 5174,
    proxy: {
      [`${DEV_SLUG}/api`]: 'http://localhost:8488',
      [`${DEV_SLUG}/login`]: 'http://localhost:8488',
      [`${DEV_SLUG}/dev-login`]: 'http://localhost:8488',
      [`${DEV_SLUG}/icon.svg`]: 'http://localhost:8488',
      '/oauth/callback': 'http://localhost:8488',
    },
  },
});
