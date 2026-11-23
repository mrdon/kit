/* Boot: last on purpose -- these are statements, and they run once
   everything above is declared. */
window.addEventListener("resize", () => { if (S) renderTrack(); });
KitLive.open({
  stream: window.__HOP_STREAM__,
  poll: window.__HOP_POLL__,
  versionUrl: window.__HOP_VERSION_URL__,
  version: window.__HOP_VERSION__,
  onFrame: (f, skew) => { clockSkew = skew; render(f); },
  onHealth,
});
tickBar();
