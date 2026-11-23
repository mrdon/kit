// Trivia chaos observer: opens the real TV and phone pages in headless Chrome
// and reports, as JSON lines on stdout, the newest game state each page has
// RECEIVED (SSE frame or poll response) and, for the TV, which screen it is
// SHOWING. The Go harness (e2e_chaos_test.go) drives the game and the network
// faults and decides pass/fail; this script only watches and obeys.
//
// stdin commands (JSON lines): {"cmd":"offline","page":"phone-1","on":true}
//                              {"cmd":"reload","page":"tv"}
//                              {"cmd":"quit"}
import { createRequire } from 'node:module';
import { readFileSync } from 'node:fs';
import readline from 'node:readline';

const require = createRequire(import.meta.url);
// playwright-core wherever it is: NODE_PATH (the global root), or nested in
// the global @playwright/cli install.
function loadPlaywright() {
  const roots = (process.env.NODE_PATH || '').split(':').filter(Boolean);
  const tries = ['playwright-core', ...roots.map((r) => r + '/@playwright/cli/node_modules/playwright-core')];
  for (const t of tries) {
    try { return require(t); } catch { /* next */ }
  }
  throw new Error('playwright-core not found; npm i -g playwright-core');
}
const { chromium } = loadPlaywright();

const cfg = JSON.parse(readFileSync(process.argv[2], 'utf8'));
const emit = (o) => process.stdout.write(JSON.stringify(o) + '\n');

const browser = await chromium.launch({ channel: 'chrome', headless: true });
const pages = {}; // name -> { page, ctx, version, phase }

async function watch(name, ctx, url) {
  const page = await ctx.newPage();
  const rec = { page, ctx, version: -1, phase: '', screen: '', polls: new Map() };
  pages[name] = rec;
  const take = (raw, via) => {
    let f;
    try { f = JSON.parse(raw); } catch { return; }
    if (typeof f.version !== 'number' || f.version <= rec.version) return;
    rec.version = f.version;
    rec.phase = f.phase;
    emit({ page: name, version: f.version, phase: f.phase, via });
  };
  const cdp = await ctx.newCDPSession(page);
  await cdp.send('Network.enable');
  cdp.on('Network.eventSourceMessageReceived', (e) => take(e.data, 'sse'));
  cdp.on('Network.loadingFinished', async (e) => {
    if (!rec.polls.has(e.requestId)) return;
    rec.polls.delete(e.requestId);
    try {
      const body = await cdp.send('Network.getResponseBody', { requestId: e.requestId });
      take(body.body, 'poll');
    } catch { /* evicted or aborted */ }
  });
  cdp.on('Network.responseReceived', (e) => {
    if (/\/state(\?|$)/.test(e.response.url) && e.response.status === 200) {
      rec.polls.set(e.requestId, true);
    }
  });
  page.on('pageerror', (err) => emit({ page: name, error: String(err) }));
  await page.goto(url, { waitUntil: 'domcontentloaded' });
}

for (const p of cfg.pages) {
  const ctx = await browser.newContext({ baseURL: cfg.base });
  if (p.cookie) {
    await ctx.addCookies([{ name: p.cookie.name, value: p.cookie.value, url: cfg.base + p.cookie.path, httpOnly: true }]);
  }
  await watch(p.name, ctx, cfg.base + p.path);
}

// The TV's rendered screen, sampled: proof the frame was not just received
// but painted.
setInterval(async () => {
  const tv = pages.tv;
  if (!tv) return;
  try {
    const id = await tv.page.evaluate(() => document.querySelector('.screen.on')?.id || '');
    if (id !== tv.screen) { tv.screen = id; emit({ page: 'tv', screen: id }); }
  } catch { /* navigating */ }
}, 250);

emit({ ready: true });

const rl = readline.createInterface({ input: process.stdin });
for await (const line of rl) {
  let c;
  try { c = JSON.parse(line); } catch { continue; }
  const rec = pages[c.page];
  if (c.cmd === 'quit') break;
  if (c.cmd === 'offline' && rec) await rec.ctx.setOffline(!!c.on);
  if (c.cmd === 'reload' && rec) await rec.page.reload({ waitUntil: 'domcontentloaded' }).catch(() => {});
  emit({ ack: c.cmd, page: c.page });
}
await browser.close();
process.exit(0);
