import { useCallback, useEffect, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import {
  api,
  type EventRecord,
  type EventsSettingsSummary,
  type EventsSiteStatus,
} from '../../api';
import { useSetChatContext } from '../../chatContext';
import { useMe } from '../../me';
import { SLUG } from '../../workspace';
import ActionMenu from '../../ActionMenu';
import EventDrawer from './EventDrawer';
import PromoLink from './PromoLink';
import { verbFor, formatWhen } from './format';

// The everyday events page: a list, with create/edit in a drawer.
//
// The form and the chat launcher are two ways into the same service methods,
// not competing surfaces. A date picker beats typing "next Thursday"; chat
// beats a form for "move it to 7pm and tighten the blurb". So the page
// registers its chat context and reloads when the agent changes something.

// openTopper opens the printable table topper in its own tab.
//
// A window.open rather than a fetch: the PDF belongs in the browser's own
// viewer, where Cmd-P is already the print button, and the session cookie goes
// along for the ride.
function openTopper(week: 'this' | 'next') {
  window.open(`/${SLUG}/events/topper.pdf?week=${week}`, '_blank', 'noopener');
}

// openSign opens today's entrance sign, the letter page for the stand by
// the door, the same way.
function openSign() {
  window.open(`/${SLUG}/events/sign.pdf?day=today`, '_blank', 'noopener');
}

// openScreen opens the wall slideshow. It is a public page, so the tab's own
// address is the one to paste into a kiosk board.
function openScreen() {
  window.open(`/${SLUG}/events/screen`, '_blank', 'noopener');
}

export default function Events() {
  const [events, setEvents] = useState<EventRecord[]>([]);
  const [settings, setSettings] = useState<EventsSettingsSummary | null>(null);
  const [open, setOpen] = useState<EventRecord | null>(null);
  // /events/:id opens that event's drawer, so a poster page can link back
  // and the browser's Back returns to the drawer, not a closed list.
  const { id: routeId } = useParams();
  const navigate = useNavigate();
  const me = useMe();
  const postersOn = !(me?.disabled_apps ?? []).includes('posters');
  const [creating, setCreating] = useState(false);
  const [includePast, setIncludePast] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [site, setSite] = useState<EventsSiteStatus | null>(null);
  const [reviewing, setReviewing] = useState(false);
  const [publishing, setPublishing] = useState(false);

  const load = useCallback(() => {
    api
      .listEvents({ include_past: includePast })
      .then((r) => {
        setEvents(r.events ?? []);
        setSettings(r.settings);
      })
      .catch((e) => setErr((e as Error).message));
    // Website state is advisory: a failure here must not break the list.
    api
      .eventsSiteStatus()
      .then(setSite)
      .catch(() => setSite(null));
  }, [includePast]);

  useEffect(load, [load]);
  useEffect(() => {
    if (!routeId || open?.id === routeId) return;
    const found = events.find((e) => e.id === routeId);
    if (found) {
      setOpen(found);
      return;
    }
    // Not in the list (a past event, or the list is still loading): fetch it.
    api
      .getEvent(routeId)
      .then((r) => setOpen(r.event))
      .catch((e) => setErr((e as Error).message));
  }, [routeId, events, open?.id]);

  const pendingCount = (site?.pending ?? []).length;

  const publishSite = async () => {
    setPublishing(true);
    setErr(null);
    try {
      const r = await api.eventsPublishSite();
      setSite(r);
      setReviewing(false);
      setNote('The website is rebuilding — it usually takes a minute or two.');
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setPublishing(false);
    }
  };

  useSetChatContext(
    open ? `the Events page, viewing "${open.title}"` : 'the Events page',
    load,
  );

  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <span>Events</span>
        </nav>
        <div className="page-head-row">
          <h1>Events</h1>
          <div className="page-head-actions">
            {/* Publishing pushes events to the public website. The calendar is
                always current, so this button is only ever about the web --
                hence the title, and the badge for the count waiting. */}
            <button
              className="btn btn-ghost"
              title="Publish events to the public website. The team calendar is always up to date and needs no action."
              onClick={() => setReviewing(true)}
              disabled={pendingCount === 0}
            >
              Publish
              {pendingCount > 0 && (
                <span className="btn-badge">{pendingCount}</span>
              )}
            </button>
            {/* Promotion is its own page: a different job, done at a
                different time. Only the count belongs here. */}
            <PromoLink />
            <button
              className="btn"
              onClick={() => {
                setCreating(true);
                setOpen(null);
              }}
            >
              New event
            </button>
            {/* Printing is a weekly errand, not an everyday one, so it lives
                under the cog rather than taking a slot in the button row. */}
            <ActionMenu
              label="Event actions"
              items={[
                {
                  label: 'Table topper — this week',
                  onClick: () => openTopper('this'),
                },
                {
                  label: 'Table topper — next week',
                  onClick: () => openTopper('next'),
                },
                {
                  label: 'Entrance sign — today',
                  onClick: openSign,
                },
                {
                  label: 'Wall screen',
                  onClick: openScreen,
                },
              ]}
            />
          </div>
        </div>
        <p className="page-sub">
          Enter an event once. Published events go on the team calendar; public
          ones also reach the website.
        </p>
      </div>

      {note && (
        <p className="banner banner-ok" onClick={() => setNote(null)}>
          {note}
        </p>
      )}
      {err && <p className="banner banner-error">{err}</p>}
      {settings && !settings.calendar_configured && (
        <p className="banner banner-error">
          Events are not syncing to Google Calendar yet — no calendar has been
          set up. <Link to="/admin/events">Finish setup</Link>.
        </p>
      )}

      <div className="toolbar">
        <label className="check">
          <input
            type="checkbox"
            checked={includePast}
            onChange={(e) => setIncludePast(e.target.checked)}
          />
          Show past and cancelled
        </label>
      </div>

      {events.length === 0 ? (
        <p className="empty">No events yet.</p>
      ) : (
        <ul className="card-list">
          {events.map((e) => (
            <li key={e.id}>
              <button
                className="row-card"
                onClick={() => {
                  setOpen(e);
                  navigate(`/events/${e.id}`);
                }}
              >
                <span className="row-card-main">
                  <span className="row-card-title">{e.title}</span>
                  <span className="row-card-meta">{formatWhen(e)}</span>
                </span>
                <span className="badge-row">
                  <span
                    className={`pill ${
                      e.status === 'published'
                        ? 'pill-ok'
                        : e.status === 'cancelled'
                          ? 'pill-error'
                          : 'pill-off'
                    }`}
                  >
                    {e.status}
                  </span>
                  {/* Only private is tagged. Public is the norm for anything
                      on this page, so tagging it labelled almost every row and
                      the badge stopped carrying information; the exception is
                      what a reader needs to spot.

                      Visibility is moot once cancelled — the event is off the
                      calendar and off the website — so a visibility pill next
                      to a red "cancelled" one just reads as a contradiction. */}
                  {e.status !== 'cancelled' && e.visibility === 'private' && (
                    <span className="pill">private</span>
                  )}
                  {e.status !== 'cancelled' && e.venue === 'offsite' && (
                    <span className="pill">offsite</span>
                  )}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}

      {reviewing && site && (
        <div className="drawer-backdrop" onClick={() => setReviewing(false)}>
          <aside className="drawer" onClick={(e) => e.stopPropagation()}>
            <button
              className="drawer-close"
              onClick={() => setReviewing(false)}
              aria-label="Close"
            >
              ×
            </button>
            <h2 className="drawer-title">Publish to the website</h2>
            <p className="field-note">
              These are the changes the public would see. Private bookings,
              drafts and staff notes are not listed — they never reach the
              website. The team calendar is already up to date either way.
            </p>

            <ul className="card-list">
              {(site.pending ?? []).map((c, i) => (
                <li key={i}>
                  <div className="row-card">
                    <span className="row-card-main">
                      <span className="row-card-title">
                        {c.title} {verbFor(c.action)}
                      </span>
                      <span className="row-card-meta">
                        {new Date(c.at).toLocaleString()}
                        {c.actor ? ` · ${c.actor}` : ''}
                      </span>
                    </span>
                  </div>
                </li>
              ))}
              {site.pending_truncated && (
                <li>
                  <span className="row-card-meta">…and more.</span>
                </li>
              )}
            </ul>

            {!site.hook_configured && (
              <p className="field-hint">
                No build hook is set up yet, so the website cannot be rebuilt.
                An admin can add one on the{' '}
                <Link to="/admin/events">Events calendar &amp; feed</Link> page.
              </p>
            )}

            <div className="drawer-actions">
              <button
                className="btn"
                onClick={publishSite}
                disabled={publishing || !site.hook_configured}
              >
                {publishing ? 'Publishing…' : 'Publish to website'}
              </button>
              <button
                className="btn btn-ghost"
                onClick={() => setReviewing(false)}
                disabled={publishing}
              >
                Not yet
              </button>
            </div>
          </aside>
        </div>
      )}

      {(creating || open) && (
        <EventDrawer
          event={open}
          postersOn={postersOn}
          defaultTimezone={settings?.timezone}
          onClose={() => {
            setCreating(false);
            setOpen(null);
            if (routeId) navigate('/events');
          }}
          onChanged={(msg, next) => {
            setNote(msg);
            setOpen(next ?? null);
            setCreating(false);
            load();
          }}
        />
      )}
    </div>
  );
}
