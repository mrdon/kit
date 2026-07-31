import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type EventsReconcilePlan, type EventsSettings } from '../api';
import { useSetChatContext } from '../chatContext';

// Admin configuration for the events app: which calendar to write to, the
// website URL pattern and feed token, and the sync controls.

export default function EventsSettingsPage() {
  useSetChatContext('the admin Events calendar & feed page');
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

  const save = (e: React.FormEvent) => {
    e.preventDefault();
    return run(async () => {
      const r = await api.saveEventsSettings({
        calendar_id: calendarID,
        timezone,
        public_url_template: urlTemplate,
      });
      apply(r.settings);
      setNote(r.warning || 'Saved.');
    });
  };

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
      setPlan(await api.eventsReconcile(false));
    });

  const applyReconcile = () =>
    run(async () => {
      const r = await api.eventsReconcile(true);
      setNote(r.message);
      setPlan(null);
      load();
    });

  const calendars = st?.calendars ?? [];
  const recent = st?.recent ?? [];

  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <Link to="/admin">Admin</Link>
          <span className="crumb-sep">/</span>
          <span>Events calendar &amp; feed</span>
        </nav>
        <h1>Events calendar &amp; feed</h1>
        <p className="page-sub">
          Choose the Google Calendar events sync to, and set up the feed your
          website builds its event pages from.
        </p>
      </div>

      {note && (
        <p className="banner banner-ok" onClick={() => setNote(null)}>
          {note}
        </p>
      )}
      {err && <p className="banner banner-error">{err}</p>}
      {!st && !err && <p className="muted">Loading…</p>}

      {st && (
        <>
          <section className="panel">
            <h2 className="panel-title">Google Calendar</h2>
            <p className="status-line">
              {st.google_connected ? (
                <span className="pill pill-ok">Google Calendar connected</span>
              ) : (
                <span className="pill pill-off">
                  Google Calendar not connected
                </span>
              )}{' '}
              {st.calendar_id ? (
                <span className="pill pill-ok">Calendar selected</span>
              ) : (
                <span className="pill pill-off">No calendar selected</span>
              )}
            </p>

            {!st.google_connected && (
              <p className="muted">
                Connect it on the{' '}
                <Link to="/admin/integrations">Integrations</Link> page first.
              </p>
            )}
            {st.calendars_error && <p className="muted">{st.calendars_error}</p>}

            <form onSubmit={save} className="stack-form">
              {calendars.length > 0 && (
                <label className="field">
                  <span>Calendar to write events to</span>
                  <select
                    value={calendarID}
                    onChange={(e) => setCalendarID(e.target.value)}
                  >
                    <option value="">Not selected — nothing will sync</option>
                    {calendars.map((c) => (
                      <option key={c.id} value={c.id} disabled={!c.writable}>
                        {c.name}
                        {c.primary ? ' (primary)' : ''}
                        {c.writable ? '' : ' — read only'}
                      </option>
                    ))}
                  </select>
                  <span className="field-note">
                    Every event goes here, private bookings included — staff and
                    the food partner read this calendar. Only the website
                    filters on visibility.
                  </span>
                </label>
              )}

              <label className="field">
                <span>Default timezone</span>
                <input
                  value={timezone}
                  onChange={(e) => setTimezone(e.target.value)}
                />
                <span className="field-note">
                  A named zone such as America/Denver, never a fixed offset —
                  the zone is what keeps a weekly 7pm event at 7pm across
                  daylight saving.
                </span>
              </label>

              <label className="field">
                <span>Website event page URL pattern</span>
                <input
                  value={urlTemplate}
                  placeholder="https://www.example.com/events/{slug}"
                  onChange={(e) => setUrlTemplate(e.target.value)}
                />
                <span className="field-note">
                  Must contain {'{slug}'}. Each event's public link is built
                  from this, so changing the domain never means rewriting past
                  events.
                </span>
              </label>

              <div className="drawer-actions">
                <button className="btn" type="submit" disabled={busy}>
                  {busy ? 'Saving…' : 'Save settings'}
                </button>
              </div>
            </form>
          </section>

          <section className="panel">
            <h2 className="panel-title">Website feed</h2>
            {st.feed_url ? (
              <>
                <p className="muted">
                  Your site's build fetches this URL, sending the token as an{' '}
                  <code>Authorization: Bearer</code> header.
                </p>
                <div className="snippet-box">
                  <pre className="snippet">{st.feed_url}</pre>
                </div>
                <div className="snippet-box">
                  <pre className="snippet">{st.feed_token}</pre>
                </div>
                <div className="drawer-actions">
                  <button
                    className="btn btn-ghost"
                    onClick={rotate}
                    disabled={busy}
                  >
                    Generate a new token
                  </button>
                </div>
                <p className="field-hint">
                  Generating a new one stops the old token working immediately,
                  so update the website build at the same time.
                </p>
              </>
            ) : (
              <>
                <p className="muted">
                  No feed token yet, so the website has nothing to fetch.
                </p>
                <div className="drawer-actions">
                  <button className="btn" onClick={rotate} disabled={busy}>
                    Generate feed token
                  </button>
                </div>
              </>
            )}
          </section>

          <section className="panel">
            <h2 className="panel-title">Sync</h2>
            <p className="muted">
              Events sync automatically every 15 minutes. Check for drift if the
              calendar has got out of step — someone deleted an entry by hand,
              say.
            </p>
            <div className="drawer-actions">
              <button className="btn" onClick={syncNow} disabled={busy}>
                Sync now
              </button>
              <button
                className="btn btn-ghost"
                onClick={preview}
                disabled={busy}
              >
                Check for drift
              </button>
            </div>

            {plan && (
              <>
                <div className="snippet-box">
                  <pre className="snippet">{plan.message}</pre>
                </div>
                {!plan.empty && (
                  <div className="drawer-actions">
                    <button
                      className="btn btn-danger"
                      onClick={applyReconcile}
                      disabled={busy}
                    >
                      Apply these changes
                    </button>
                  </div>
                )}
              </>
            )}

            <h3 className="panel-title">Recent syncs</h3>
            {recent.length === 0 ? (
              <p className="empty">No syncs recorded yet.</p>
            ) : (
              <ul className="card-list">
                {recent.map((r, i) => (
                  <li key={i}>
                    <div className="row-card">
                      <span className="row-card-main">
                        <span className="row-card-title">
                          {r.ok
                            ? `${r.created} created, ${r.updated} updated, ${r.deleted} removed`
                            : `Failed: ${r.error}`}
                        </span>
                        <span className="row-card-sub">
                          {r.at} · {r.triggered_by}
                        </span>
                      </span>
                      <span className={`pill ${r.ok ? 'pill-ok' : 'pill-error'}`}>
                        {r.ok ? 'ok' : 'failed'}
                      </span>
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </section>
        </>
      )}
    </div>
  );
}
