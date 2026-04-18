import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// Kit serves the built app from Go at /app/ with SPA fallback handled in
// the Go handler. base must match.
export default defineConfig({
  plugins: [react()],
  base: '/app/',
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    // Inline assets so the go:embed tree stays shallow.
    assetsInlineLimit: 0,
  },
  server: {
    port: 5173,
    proxy: {
      // Dev server proxies API + login to the Go backend.
      '/api': 'http://localhost:8488',
      '/app/dev-login': 'http://localhost:8488',
      '/app/login': 'http://localhost:8488',
      '/app/callback': 'http://localhost:8488',
    },
  },
});
