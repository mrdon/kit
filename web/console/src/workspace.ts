// BASENAME is the path prefix the console SPA is mounted under, derived
// from the browser URL at boot. The console lives at /{slug}/web/* (the
// segment is provisional — held in a single Go constant server-side and
// here in CONSOLE_SEGMENT), so the React router's basename is
// /{slug}/web.
//
// The server rejects invalid slugs with 404, so the validation here is
// just a sanity check to avoid constructing garbage URLs if the bundle
// is ever mounted somewhere unexpected.
const slugPattern = /^[a-z0-9][a-z0-9-]{0,62}$/;

// Keep in sync with consoleSegment in cmd/kit/main.go.
export const CONSOLE_SEGMENT = 'web';

const parts = location.pathname.split('/');
const slug = parts[1] ?? '';
if (!slugPattern.test(slug) || parts[2] !== CONSOLE_SEGMENT) {
  throw new Error(
    `Console mounted under unexpected path: ${location.pathname}`,
  );
}

export const SLUG = slug;
export const BASENAME = `/${slug}/${CONSOLE_SEGMENT}`;
