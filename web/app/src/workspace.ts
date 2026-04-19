// BASENAME is the workspace slug prefix the PWA is served under, derived
// from the browser URL at boot. Every workspace gets its own PWA install
// on Android (distinct manifest id/scope), and asset + API paths are
// rooted at /{slug}/...
//
// The server rejects invalid slugs with 404, so the client validation
// here is just a sanity check to keep us from constructing garbage URLs
// (e.g. if something mounted the app under "/" directly, we fail loud).
const slugPattern = /^[a-z0-9][a-z0-9-]{0,62}$/;

const seg = location.pathname.split('/')[1] ?? '';
if (!slugPattern.test(seg)) {
  throw new Error(
    `PWA mounted under invalid workspace path: ${location.pathname}`,
  );
}

export const BASENAME = '/' + seg;

// Expose the workspace's icon URL as a CSS variable so styles.css can
// paint it as a subtle background watermark. Helps the user see at a
// glance which workspace they're in when multiple PWA installs exist.
document.documentElement.style.setProperty(
  '--workspace-icon',
  `url(${BASENAME}/icon-512.png)`,
);
