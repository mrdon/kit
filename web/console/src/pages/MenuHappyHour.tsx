import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type HappyHour, type HappyHourConfig } from '../api';
import { useSetChatContext } from '../chatContext';

// Happy hour settings.
//
// Happy hour is on or off right now, and that is what the top of the page
// shows and controls. The schedule switches it at each start and end time;
// Start now / End now switch it by hand and hold until the next scheduled
// start or end. The board and Square both follow the state by themselves --
// Square within a minute, or at once after Start now / End now.
//
// The log of the last Square push is always on the page, Square's own error
// text included: the most likely failure is a token without catalog
// permission, and that is only fixable if someone can read what Square said.

const DAYS: [string, string][] = [
  ['mon', 'Mon'],
  ['tue', 'Tue'],
  ['wed', 'Wed'],
  ['thu', 'Thu'],
  ['fri', 'Fri'],
  ['sat', 'Sat'],
  ['sun', 'Sun'],
];

function priceText(cents: number): string {
  return cents % 100 === 0 ? String(cents / 100) : (cents / 100).toFixed(2);
}

export default function MenuHappyHour() {
  useSetChatContext('the happy hour settings page');
  const [data, setData] = useState<HappyHour | null>(null);
  const [cfg, setCfg] = useState<HappyHourConfig | null>(null);
  const [price, setPrice] = useState('');
  const [err, setErr] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [syncing, setSyncing] = useState<'preview' | 'apply' | 'now' | null>(null);
  const [log, setLog] = useState<{ text: string; ok: boolean; applied: boolean } | null>(null);

  function load(d: HappyHour) {
    setData(d);
    setCfg(d.config);
    setPrice(priceText(d.config.price_cents));
  }

  useEffect(() => {
    api
      .happyHour()
      .then(load)
      .catch((e) => setErr(e instanceof Error ? e.message : String(e)));
  }, []);

  function set<K extends keyof HappyHourConfig>(key: K, value: HappyHourConfig[K]) {
    setCfg((c) => (c ? { ...c, [key]: value } : c));
    setSaved(false);
  }

  function toggle(list: string[], item: string): string[] {
    return list.includes(item) ? list.filter((x) => x !== item) : [...list, item];
  }

  // The switch saves on its own. A switch that only flips a form field, with
  // the Save button a section further down, reads as broken: someone turned
  // happy hour "on", walked away, and it was still off.
  async function save(override?: Partial<HappyHourConfig>) {
    if (!cfg) return;
    const cents = Math.round(parseFloat(price.replace('$', '')) * 100);
    if (!(cents > 0)) {
      setErr('The price should be an amount like 5 or 5.50.');
      return;
    }
    setSaving(true);
    setErr(null);
    try {
      load(await api.saveHappyHour({ ...cfg, ...override, price_cents: cents }));
      setSaved(true);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  }

  async function startStop(on: boolean) {
    setSyncing('now');
    setErr(null);
    try {
      const res = await api.happyHourNow(on);
      load(res.state);
      setLog({ text: res.log, ok: res.ok, applied: true });
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setSyncing(null);
    }
  }

  async function sync(apply: boolean) {
    setSyncing(apply ? 'apply' : 'preview');
    setErr(null);
    try {
      const res = await api.syncHappyHour(apply);
      setData(res.state);
      setLog({ text: res.log, ok: res.ok, applied: res.applied });
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setSyncing(null);
    }
  }

  // Beers configured but not on the board still need a checkbox, or there
  // would be no way to take them off the list.
  const tapNames = data?.taps.map((t) => t.name) ?? [];
  const offBoard = (cfg?.beers ?? []).filter(
    (b) => !tapNames.some((n) => n.toLowerCase() === b.toLowerCase()),
  );
  const dirty = data && cfg && JSON.stringify(cfg) !== JSON.stringify(data.config);

  let squareStatus = 'Nothing has been sent to Square yet.';
  if (data?.synced_at) {
    const when = new Date(data.synced_at).toLocaleString();
    if (data.in_sync) squareStatus = `Square matches · last updated ${when}`;
    else if (!data.sync_ok) squareStatus = `The last Square update failed (${when}). The log is below; Kit retries every 10 minutes.`;
    else squareStatus = `Square is catching up; Kit updates it within a minute.`;
  }
  const shownLog = log?.text ?? data?.sync_log ?? '';

  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <Link to="/admin">Admin</Link>
          <span className="crumb-sep">/</span>
          <span>Happy hour</span>
        </nav>
        <h1>Happy hour</h1>
        <p className="page-sub">
          A fixed price on a few beers, on a weekly schedule. The menu board
          follows this as soon as you save. Square changes only when you sync.
        </p>
      </div>

      {err && <p className="banner banner-error">{err}</p>}
      {!cfg && !err && <p className="muted">Loading…</p>}

      {cfg && data && (
        <>
          <section className="panel">
            <h2 className="panel-title">
              {data.on_now
                ? `Happy hour is on${data.until ? ` until ${data.until}` : ''}`
                : 'Happy hour is off'}
            </h2>
            <p className="card-desc">
              On the menu board and in Square. Start now and End now hold until
              the next scheduled start or end
              {data.on_now && !data.until ? ', or until you end it' : ''}.
            </p>
            <div className="drawer-actions">
              {data.on_now ? (
                <button className="btn" onClick={() => startStop(false)} disabled={syncing !== null}>
                  {syncing === 'now' ? 'Ending…' : 'End now'}
                </button>
              ) : (
                <button
                  className="btn"
                  onClick={() => startStop(true)}
                  disabled={syncing !== null || !data.configured || cfg.beers.length === 0}
                >
                  {syncing === 'now' ? 'Starting…' : 'Start now'}
                </button>
              )}
            </div>
            {!data.configured && (
              <p className="field-note">Pick the beers and save before starting it.</p>
            )}
          </section>

          <section className="panel">
            <h2 className="panel-title">Schedule</h2>
            <label className="switch">
              <input
                type="checkbox"
                checked={cfg.enabled}
                disabled={saving}
                onChange={(e) => save({ enabled: e.target.checked })}
              />
              <span className="switch-track" aria-hidden="true" />
              <span>
                {cfg.enabled
                  ? 'Runs on a schedule: on at the start time, off at the end'
                  : 'No schedule: only Start now turns it on'}
              </span>
            </label>
            {err && <p className="banner banner-error">{err}</p>}

            <div className="field-row">
              {DAYS.map(([code, label]) => (
                <label className="check" key={code}>
                  <input
                    type="checkbox"
                    checked={cfg.days.includes(code)}
                    onChange={() => set('days', toggle(cfg.days, code))}
                  />
                  {label}
                </label>
              ))}
            </div>

            <div className="field-row">
              <label className="field">
                <span>Starts</span>
                <input type="time" value={cfg.start} onChange={(e) => set('start', e.target.value)} />
              </label>
              <label className="field">
                <span>Ends</span>
                <input type="time" value={cfg.end} onChange={(e) => set('end', e.target.value)} />
              </label>
              <label className="field">
                <span>First scheduled day</span>
                <input
                  type="date"
                  value={cfg.starts_on ?? ''}
                  onChange={(e) => set('starts_on', e.target.value)}
                />
              </label>
            </div>

            <div className="field-row">
              <label className="field">
                <span>Price</span>
                <input
                  value={price}
                  inputMode="decimal"
                  onChange={(e) => {
                    setPrice(e.target.value);
                    setSaved(false);
                  }}
                />
              </label>
              <label className="field">
                <span>Pour (the Square variation name)</span>
                <input value={cfg.size} onChange={(e) => set('size', e.target.value)} />
              </label>
            </div>
            <p className="field-note">
              Times are in the workspace timezone{data.timezone ? ` (${data.timezone})` : ''}.
            </p>
          </section>

          <section className="panel">
            <h2 className="panel-title">Beers</h2>
            <p className="card-desc">
              Pick from what is on the board. In Square, each one has to match
              an item&rsquo;s name or kitchen name.
            </p>
            {data.taps.map((t) => (
              <label className="check" key={t.name} style={{ display: 'flex' }}>
                <input
                  type="checkbox"
                  checked={cfg.beers.some((b) => b.toLowerCase() === t.name.toLowerCase())}
                  onChange={() => set('beers', toggle(cfg.beers, t.name))}
                />
                {t.name} · {t.size} · {t.price}
              </label>
            ))}
            {offBoard.map((b) => (
              <label className="check" key={b} style={{ display: 'flex' }}>
                <input
                  type="checkbox"
                  checked
                  onChange={() => set('beers', toggle(cfg.beers, b))}
                />
                {b} · not on the board right now
              </label>
            ))}
            <div className="drawer-actions">
              <button className="btn" onClick={() => save()} disabled={saving}>
                {saving ? 'Saving…' : 'Save'}
              </button>
              {saved && <span className="muted">Saved. The board follows it now.</span>}
              {err && <span className="banner banner-error">{err}</span>}
            </div>
          </section>

          <section className="panel">
            <h2 className="panel-title">Square</h2>
            <p className="card-desc">
              While happy hour is on, Square applies an automatic discount to
              each beer&rsquo;s pour, sized to land on the happy hour price.
              Kit keeps it in step by itself. Preview shows what Square rings;
              Push now updates Square straight away and shows what it said.
            </p>
            <p className="card-desc">{squareStatus}</p>
            {dirty && (
              <p className="banner">Save first: Square follows the saved setting.</p>
            )}
            <div className="drawer-actions">
              <button
                className="btn btn-ghost"
                onClick={() => sync(false)}
                disabled={syncing !== null || !data.configured}
              >
                {syncing === 'preview' ? 'Checking…' : 'Preview'}
              </button>
              <button
                className="btn"
                onClick={() => sync(true)}
                disabled={syncing !== null || !data.configured || !!dirty}
              >
                {syncing === 'apply' ? 'Pushing…' : 'Push to Square now'}
              </button>
            </div>
            {shownLog && (
              <>
                <p className="card-desc">
                  {log
                    ? `${log.applied ? 'Square update' : 'Preview'} ${log.ok ? 'finished' : 'did not finish'}:`
                    : 'Last Square update:'}
                </p>
                <pre
                  className={log && !log.ok ? 'banner banner-error' : 'banner'}
                  style={{ whiteSpace: 'pre-wrap', fontSize: '0.85rem' }}
                >
                  {shownLog}
                </pre>
              </>
            )}
          </section>
        </>
      )}
    </div>
  );
}
