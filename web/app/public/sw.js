// Minimal app-shell service worker.
//
// Each workspace PWA install has its own scope (/{slug}/) and its own
// cache namespace keyed on that scope — two installs on the same device
// cannot share cached bytes across workspaces.
//
// Caching strategy:
//   - Workspace HTML (the entry + SPA fallback under /{slug}/...) is
//     network-first with a cache fallback. Deploys take effect on the
//     next refresh instead of requiring several to flush.
//   - The shared hashed bundle at /app/assets/... is cache-first —
//     Vite's content-hash filenames make the URLs effectively immutable.
//   - API routes are never touched: they are authenticated and tenant
//     scoped, and any cached response would leak across users or
//     workspaces.

// SCOPE is the path prefix this SW registration covers, e.g.
// "/gravity-brewing/". Derived from registration.scope so the same SW
// source works for every workspace.
const SCOPE_URL = new URL(self.registration.scope);
const SCOPE = SCOPE_URL.pathname; // trailing slash included
const CACHE = 'kit' + SCOPE + 'v4';
// Shell is intentionally minimal — manifest and icons are fetched
// straight from the network so install-time icon changes aren't
// masked by a stale cached shell.
const SHELL = [SCOPE];

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

  // Never cache authenticated API routes. After the /{slug}/ migration
  // these live under /{slug}/api/v1/... — the literal "/api/v1/"
  // substring match catches them regardless of workspace prefix.
  if (url.pathname.includes('/api/v1/')) return;
  // Never cache login / OAuth handoffs.
  if (url.pathname.endsWith('/login')) return;
  if (url.pathname.endsWith('/dev-login')) return;
  if (url.pathname.startsWith('/oauth/')) return;
  // Per-tenant OAuth endpoints (/{slug}/oauth/authorize, /token, /register,
  // and the /{slug}/.well-known/... discovery doc) must never be cached.
  if (url.pathname.includes('/oauth/') || url.pathname.includes('/.well-known/')) return;
  // Never cache the manifest or tenant icons — they can change when a
  // workspace re-OAuths (new Slack team icon, updated slug, etc.).
  if (url.pathname.endsWith('/manifest.webmanifest')) return;
  if (url.pathname.endsWith('/icon-192.png')) return;
  if (url.pathname.endsWith('/icon-512.png')) return;
  if (url.pathname.endsWith('/icon.svg')) return;

  if (e.request.method !== 'GET') return;

  const inScope = url.pathname.startsWith(SCOPE);
  const isSharedBundle = url.pathname.startsWith('/app/assets/');
  if (!inScope && !isSharedBundle) return;

  const isNavigation =
    e.request.mode === 'navigate' ||
    url.pathname === SCOPE ||
    url.pathname === SCOPE.replace(/\/$/, '') ||
    url.pathname.endsWith('.html');

  if (isNavigation) {
    // Network-first: always try for fresh HTML so a new deploy's
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
        .catch(() =>
          caches.match(e.request).then((cached) => cached || Response.error()),
        ),
    );
    return;
  }

  // Hashed bundle + static per-workspace assets: cache-first.
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
