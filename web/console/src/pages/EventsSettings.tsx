import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type EventsReconcilePlan, type EventsSettings } from '../api';
import { useSetChatContext } from '../chatContext';

// Admin configuration for the events app: which calendar to write to, the
// website URL pattern, the feed token, and the sync controls.

export default function EventsSettingsPage() {
  useSetChatContext('the admin Events settings page');
  const [st, setSt] = useState<EventsSettings | null>(null);
  const [calendarID, setCalendarID] = useState('');
  const [timezone, setTimezone] = useState('');
  const [urlTemplate, setUrlTemplate] = useState('');
  const [plan, setPlan] = useState<EventsReconcilePlan | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const apply = (next: EventsSettings) => {
    setSt(next);
    setCalendarID(next.calendar_id);
    setTimezone(next.timezone);
    setUrlTemplate(next.public_url_template);
  };

  const load = () => {
    api
      .eventsSettings()
      .then(apply)
      .catch((e) => setErr((e as Error).message));
  };
  useEffect(load, []);

  const run = async (fn: () => Promise<void>) => {
    setBusy(true);
    setErr(null);
    setNote(null);
    try {
      await fn();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const save = () =>
    run(async () => {
      const r = await api.saveEventsSettings({
        calendar_id: calendarID,
        timezone,
        public_url_template: urlTemplate,
      });
      apply(r.settings);
      setNote(r.warning || 'Saved.');
    });

  const rotate = () =>
    run(async () => {
      const r = await api.rotateEventsFeedToken();
      apply(r.settings);
      setNote(r.warning);
    });

  const syncNow = () =>
    run(async () => {
      const r = await api.eventsSyncNow();
      setNote(r.message);
      load();
    });

  const preview = () =>
    run(async () => {
      const r = await api.eventsReconcile(false);
      setPlan(r);
    });

  const applyReconcile = () =>
    run(async () => {
      const r = await api.eventsReconcile(true);
      setNote(r.message);
      setPlan(null);
      load();
    });

  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <Link to="/admin">Admin</Link>
          <span className="crumb-sep">/</span>
          <span>Events</span>
        </nav>
        <h1>Events settings</h1>
      </div>

      {err && <div className="alert alert-error">{err}</div>}
      {note && <div className="alert">{note}</div>}

      <section className="card">
        <h2>Google Calendar</h2>
        {!st?.google_connected && (
          <p className="muted">
            Google Calendar is not connected.{' '}
            <Link to="/admin/integrations">Connect it on Integrations</Link>,
            then share your events calendar with the service account.
          </p>
        )}
        {st?.calendars_error && <p className="muted">{st.calendars_error}</p>}
        {st && (st.calendars?.length ?? 0) > 0 && (
          <label>
            Calendar to write events to
            <select
              value={calendarID}
              onChange={(e) => setCalendarID(e.target.value)}
            >
              <option value="">Not selected — nothing will sync</option>
              {(st.calendars ?? []).map((c) => (
                <option key={c.id} value={c.id} disabled={!c.writable}>
                  {c.name}
                  {c.primary ? ' (primary)' : ''}
                  {c.writable ? '' : ' — read only'}
                </option>
              ))}
            </select>
            <span className="hint">
              All events go here, private bookings included — staff and the food
              partner read this calendar. Only the website filters on
              visibility.
            </span>
          </label>
        )}

        <label>
          Default timezone
          <input value={timezone} onChange={(e) => setTimezone(e.target.value)} />
          <span className="hint">
            A named zone such as America/Denver, never a fixed offset — the zone
            is what keeps a weekly 7pm event at 7pm across daylight saving.
          </span>
        </label>
      </section>

      <section className="card">
        <h2>Website</h2>
        <label>
          Event page URL pattern
          <input
            value={urlTemplate}
            placeholder="https://www.example.com/events/{slug}"
            onChange={(e) => setUrlTemplate(e.target.value)}
          />
          <span className="hint">
            Must contain {'{slug}'}. Each event's link is built from this, so
            changing the domain never means rewriting past events.
          </span>
        </label>

        {/* Always rendered, including before a token exists — otherwise an
            admin looking for the feed URL finds nothing and no way forward. */}
        {st?.feed_url ? (
          <>
            <label>
              Feed URL
              <input readOnly value={st.feed_url} />
              <span className="hint">
                The website build fetches this, sending the token below as an{' '}
                <code>Authorization: Bearer</code> header.
              </span>
            </label>
            <label>
              Feed token
              <input readOnly value={st.feed_token ?? ''} />
            </label>
            <button className="btn btn-secondary" onClick={rotate} disabled={busy}>
              Generate a new token
            </button>
          </>
        ) : (
          <>
            <p className="muted">
              No feed token yet, so the website has nothing to fetch. Generate
              one and copy it, with the URL, into your site's build settings.
            </p>
            <button className="btn" onClick={rotate} disabled={busy}>
              Generate feed token
            </button>
          </>
        )}
      </section>

      <section className="card">
        <h2>Sync</h2>
        <div className="actions">
          <button className="btn" onClick={save} disabled={busy}>
            Save settings
          </button>
          <button className="btn btn-secondary" onClick={syncNow} disabled={busy}>
            Sync now
          </button>
          <button className="btn btn-secondary" onClick={preview} disabled={busy}>
            Check for drift
          </button>
        </div>

        {plan && (
          <div className="alert">
            <pre className="plan">{plan.message}</pre>
            {!plan.empty && (
              <button className="btn" onClick={applyReconcile} disabled={busy}>
                Apply these changes
              </button>
            )}
          </div>
        )}

        <h3>Recent syncs</h3>
        {!st?.recent?.length && <p className="muted">No syncs recorded yet.</p>}
        <ul className="rows">
          {st?.recent?.map((r, i) => (
            <li key={i} className="row">
              <span>{r.at}</span>
              <span className="muted">{r.triggered_by}</span>
              {r.ok ? (
                <span>
                  {r.created} created, {r.updated} updated, {r.deleted} removed
                </span>
              ) : (
                <span className="pill pill-off">failed: {r.error}</span>
              )}
            </li>
          ))}
        </ul>
      </section>
    </div>
  );
}
