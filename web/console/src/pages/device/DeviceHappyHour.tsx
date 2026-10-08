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

  const canStart = !!data && data.configured && data.config.beers.length > 0;

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
          {!canStart && !data.on_now && (
            <p className="page-sub">Nothing to start yet: an admin needs to pick the beers and the price first.</p>
          )}
          {data.config.beers.length > 0 && (
            <p className="page-sub">
              {data.config.beers.join(', ')} · {data.config.size || 'pint'}
            </p>
          )}
        </section>
      )}
    </div>
  );
}
