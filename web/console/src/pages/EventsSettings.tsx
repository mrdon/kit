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

  // Empty is the normal case for a service account, not an error, so the
  // listing is a convenience only and its failure is never surfaced.
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
            {/* A service account cannot list calendars shared with it -- its
                calendarList is its own subscription list, and there is no
                invite for it to accept -- so the id is typed, not picked, and
                the status has to describe that rather than imply a dropdown. */}
            {!st.google_connected ? (
              <p className="banner banner-error">
                Google Calendar is not connected. Connect it on the{' '}
                <Link to="/admin/integrations">Integrations</Link> page first.
              </p>
            ) : st.calendar_id ? (
              <p className="status-line">
                <span className="pill pill-ok">Syncing</span> Events are written
                to the calendar below.
              </p>
            ) : (
              <p className="status-line">
                <span className="pill pill-off">Not syncing</span> Kit can reach
                Google Calendar. Point it at a calendar below to start.
              </p>
            )}

            <form onSubmit={save} className="stack-form">
              <label className="field">
                <span>Calendar ID</span>
                <input
                  value={calendarID}
                  placeholder="something@group.calendar.google.com"
                  onChange={(e) => setCalendarID(e.target.value)}
                />
                <span className="field-note">
                  In Google Calendar, open the events calendar&apos;s settings.
                  Under <em>Share with specific people</em> add the address
                  below with <em>Make changes to events</em>, then copy the{' '}
                  <em>Calendar ID</em> from further down the same page.
                </span>
                {st.service_account_email && (
                  <div className="snippet-box">
                    <pre className="snippet">{st.service_account_email}</pre>
                  </div>
                )}
                <span className="field-note">
                  Every event goes here, private bookings included — staff and
                  the food partner read this calendar. Only the website filters
                  on visibility. Saving checks Kit can write to it.
                </span>
              </label>

              {calendars.length > 0 && (
                <label className="field">
                  <span>Or pick one Kit can see</span>
                  <select
                    value={calendars.some((c) => c.id === calendarID) ? calendarID : ''}
                    onChange={(e) => setCalendarID(e.target.value)}
                  >
                    <option value="">Choose…</option>
                    {calendars.map((c) => (
                      <option key={c.id} value={c.id} disabled={!c.writable}>
                        {c.name}
                        {c.primary ? ' (primary)' : ''}
                        {c.writable ? '' : ' — read only'}
                      </option>
                    ))}
                  </select>
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
                        <span className="row-card-meta">
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
