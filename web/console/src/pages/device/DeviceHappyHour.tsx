import { useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type HappyHour } from '../../api';
import { useSetChatContext } from '../../chatContext';
import { usePolled } from '../../usePolled';

// Happy hour, from the bar iPad: is it on, and one big button to flip it.
// Same Start now / End now the admin page has, without the schedule and
// price editing -- those are decisions, this is a switch somebody reaches
// for between pours. Square is pushed in the same request and its answer
// is shown, so a pint rung at the wrong price is caught here, not at the
// till.

export default function DeviceHappyHour() {
  useSetChatContext('the happy hour switch on a taproom device');
  const [data, setData] = useState<HappyHour | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<{ text: string; ok: boolean } | null>(null);
  const [saving, setSaving] = useState(false);

  // Polled: happy hour is ended from Slack, the schedule, or the other
  // iPad as often as from here, and a Start button over an already-on
  // happy hour is the lie this page exists to avoid.
  usePolled(() =>
    api
      .happyHour()
      .then(setData)
      .catch((e) => setErr(e instanceof Error ? e.message : String(e))),
  );

  async function flip(on: boolean) {
    setBusy(true);
    setErr(null);
    setNote(null);
    try {
      const res = await api.happyHourNow(on);
      setData(res.state);
      setNote({
        text: res.ok ? 'Square has the new prices.' : 'Square did not take the change. The board changed; tell a manager.',
        ok: res.ok,
      });
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  // Which beers are on happy hour. Each tick saves the whole setting back
  // with the beers changed, the same call the admin page makes, so the
  // schedule and price it was given stay as they were. While happy hour is
  // on, Square follows within a minute.
  async function toggleBeer(name: string) {
    if (!data || saving) return;
    const same = (a: string, b: string) => a.trim().toLowerCase() === b.trim().toLowerCase();
    const on = data.config.beers.some((b) => same(b, name));
    const beers = on ? data.config.beers.filter((b) => !same(b, name)) : [...data.config.beers, name];
    setSaving(true);
    setErr(null);
    try {
      setData(await api.saveHappyHour({ ...data.config, beers }));
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  }

  const canStart = !!data && data.configured && data.config.beers.length > 0;
  const tapNames = data?.taps.map((t) => t.name) ?? [];
  const offBoard = (data?.config.beers ?? []).filter(
    (b) => !tapNames.some((n) => n.trim().toLowerCase() === b.trim().toLowerCase()),
  );

  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <span>Happy hour</span>
        </nav>
        <h1>{data ? (data.on_now ? `Happy hour is on${data.until ? ` until ${data.until}` : ''}` : 'Happy hour is off') : 'Happy hour'}</h1>
        <p className="page-sub">
          Start now and End now hold until the next scheduled start or end. The board and Square follow at once.
        </p>
      </div>
      {err && <p className="banner banner-error">{err}</p>}
      {note && <p className={note.ok ? 'banner banner-ok' : 'banner banner-error'}>{note.text}</p>}
      {data && (
        <section className="device-actions">
          {data.on_now ? (
            <button className="btn device-btn device-btn-stop" onClick={() => void flip(false)} disabled={busy}>
              {busy ? 'Ending…' : 'End happy hour'}
            </button>
          ) : (
            <button className="btn device-btn" onClick={() => void flip(true)} disabled={busy || !canStart}>
              {busy ? 'Starting…' : 'Start happy hour'}
            </button>
          )}
          {!data.configured && (
            <p className="page-sub">Nothing to start yet: an admin needs to set the price and the hours first.</p>
          )}
          {data.configured && data.config.beers.length === 0 && (
            <p className="page-sub">Tick the beers below before starting it.</p>
          )}
        </section>
      )}
      {data && (
        <section className="panel device-checklist">
          <h2 className="panel-title">
            Beers on happy hour{data.config.size ? ` · ${data.config.size}` : ''}
          </h2>
          <p className="card-desc">
            Tick a beer to put it on happy hour at the set price; untick to take it off. Each tick saves straight away.
          </p>
          {data.taps.length === 0 && offBoard.length === 0 && <p className="muted">Nothing on the board yet.</p>}
          {data.taps.map((t) => (
            <label className="check" key={t.name} style={{ display: 'flex' }}>
              <input
                type="checkbox"
                checked={data.config.beers.some((b) => b.trim().toLowerCase() === t.name.trim().toLowerCase())}
                disabled={saving || !data.configured}
                onChange={() => void toggleBeer(t.name)}
              />
              {t.name}
              {t.price ? <span className="muted"> · {t.price} {t.size}</span> : null}
            </label>
          ))}
          {offBoard.map((b) => (
            <label className="check" key={b} style={{ display: 'flex' }}>
              <input type="checkbox" checked disabled={saving || !data.configured} onChange={() => void toggleBeer(b)} />
              {b} <span className="muted"> · not on tap right now</span>
            </label>
          ))}
          {saving && <p className="muted">Saving…</p>}
        </section>
      )}
    </div>
  );
}
