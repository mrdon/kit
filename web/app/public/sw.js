// Minimal app-shell service worker. Caches the built HTML/JS/CSS so the
// PWA boots offline and loses nothing if the user swipes with a flaky
// connection. No offline write queue yet — see plan Open Question #5.

const CACHE = 'kit-app-v1';
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
  if (url.pathname.startsWith('/api/') || url.pathname.startsWith('/app/login') || url.pathname.startsWith('/app/callback') || url.pathname.startsWith('/app/dev-login')) {
    return;
  }
  if (!url.pathname.startsWith('/app/')) return;

  // Stale-while-revalidate for the app shell.
  e.respondWith(
    caches.match(e.request).then((cached) => {
      const fetchPromise = fetch(e.request)
        .then((resp) => {
          if (resp.ok && e.request.method === 'GET') {
            caches.open(CACHE).then((c) => c.put(e.request, resp.clone()));
          }
          return resp;
        })
        .catch(() => cached);
      return cached || fetchPromise;
    }),
  );
});
