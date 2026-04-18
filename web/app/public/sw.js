// Minimal app-shell service worker.
//
// Caching strategy:
//   - Navigation requests (the HTML entry) are network-first with
//     a cache fallback. Deploys take effect on the next refresh
//     instead of requiring several to flush stale-while-revalidate.
//   - Hashed assets (index-*.js, index-*.css, etc.) are cache-first
//     forever — vite's content-hash filenames mean a new build
//     produces new URLs, so stale content is impossible.
//   - API + auth routes are never touched.
//
// No offline write queue yet.

const CACHE = 'kit-app-v2';
const SHELL = ['/app/', '/app/manifest.webmanifest', '/app/icon.svg'];

self.addEventListener('install', (e) => {
  e.waitUntil(caches.open(CACHE).then((c) => c.addAll(SHELL)));
  self.skipWaiting();
});

self.addEventListener('activate', (e) => {
  e.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))),
    ),
  );
  self.clients.claim();
});

self.addEventListener('fetch', (e) => {
  const url = new URL(e.request.url);

  // Never cache API or login routes.
  if (
    url.pathname.startsWith('/api/') ||
    url.pathname.startsWith('/app/login') ||
    url.pathname.startsWith('/app/callback') ||
    url.pathname.startsWith('/app/dev-login')
  ) {
    return;
  }
  if (!url.pathname.startsWith('/app/')) return;
  if (e.request.method !== 'GET') return;

  const isNavigation =
    e.request.mode === 'navigate' ||
    url.pathname === '/app/' ||
    url.pathname === '/app' ||
    url.pathname.endsWith('.html');

  if (isNavigation) {
    // Network-first: always try for a fresh HTML so a new deploy's
    // assets get discovered on the next refresh. Fall back to cache
    // only if offline.
    e.respondWith(
      fetch(e.request)
        .then((resp) => {
          if (resp.ok) {
            const clone = resp.clone();
            caches.open(CACHE).then((c) => c.put(e.request, clone));
          }
          return resp;
        })
        .catch(() => caches.match(e.request).then((cached) => cached || Response.error())),
    );
    return;
  }

  // Everything else under /app/ (hashed JS/CSS/images) is
  // cache-first. Hashed filenames make these effectively immutable,
  // so serving from cache forever is correct and fast.
  e.respondWith(
    caches.match(e.request).then((cached) => {
      if (cached) return cached;
      return fetch(e.request).then((resp) => {
        if (resp.ok) {
          const clone = resp.clone();
          caches.open(CACHE).then((c) => c.put(e.request, clone));
        }
        return resp;
      });
    }),
  );
});
