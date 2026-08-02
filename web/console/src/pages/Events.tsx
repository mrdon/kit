import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  api,
  type EventInput,
  type EventRecord,
  type EventsSettingsSummary,
  type EventsSiteStatus,
} from '../api';
import { useSetChatContext } from '../chatContext';

// The everyday events page: a list, with create/edit in a drawer.
//
// The form and the chat launcher are two ways into the same service methods,
// not competing surfaces. A date picker beats typing "next Thursday"; chat
// beats a form for "move it to 7pm and tighten the blurb". So the page
// registers its chat context and reloads when the agent changes something.

const EMPTY_FORM: EventInput = {
  title: '',
  starts_at: '',
  ends_at: '',
  summary: '',
  description: '',
  prep_notes: '',
  location: '',
  visibility: 'private',
  venue: 'onsite',
  space_impact: 'none',
  repeat_rule: '',
  registration_url: '',
};

// The API returns an instant in UTC; <input type="datetime-local"> holds a
// naive wall-clock, and the server parses what it gets back in the EVENT'S
// timezone. So the value has to be rendered in that zone -- slicing the ISO
// string instead shows the UTC wall time, and saving any other field then
// writes that wrong time back and silently moves the event. That is a data
// bug, not a display one.
function toLocalInput(iso: string | undefined, tz: string | undefined): string {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  const p = new Intl.DateTimeFormat('en-CA', {
    timeZone: tz || undefined,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  })
    .formatToParts(d)
    .reduce<Record<string, string>>((a, x) => ((a[x.type] = x.value), a), {});
  return `${p.year}-${p.month}-${p.day}T${p.hour}:${p.minute}`;
}

// Mirrors PendingChange.Verb() on the server so both surfaces use the same
// words for the same action.
const VERBS: Record<string, string> = {
  'events.event_created': 'added',
  'events.event_updated': 'edited',
  'events.event_published': 'published',
  'events.event_unpublished': 'unpublished',
  'events.event_cancelled': 'cancelled',
  'events.event_deleted': 'deleted',
};
const verbFor = (action: string) => VERBS[action] ?? 'changed';

// Spelled out because "published" is routinely misread as "public". They are
// separate axes: a confirmed private booking is published AND private.
function describeExposure(e: EventRecord): string {
  if (e.status === 'draft') return 'Not visible anywhere yet';
  if (e.status === 'cancelled') return 'Removed from the calendar and website';
  if (e.visibility === 'public') return 'On the calendar and the public website';
  return 'On the team calendar only, not public';
}

function formatWhen(e: EventRecord): string {
  const d = new Date(e.starts_at);
  const base = d.toLocaleString(undefined, {
    // The event's own zone, not the reader's: a manager checking the roster
    // from another state still needs the time the doors actually open.
    timeZone: e.timezone || undefined,
    weekday: 'short',
    day: 'numeric',
    month: 'short',
    ...(e.all_day ? {} : { hour: 'numeric', minute: '2-digit' }),
  });
  return e.rrule ? `${base} · repeats weekly` : base;
}

export default function Events() {
  const [events, setEvents] = useState<EventRecord[]>([]);
  const [settings, setSettings] = useState<EventsSettingsSummary | null>(null);
  const [open, setOpen] = useState<EventRecord | null>(null);
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
            <button
              className="btn"
              onClick={() => {
                setCreating(true);
                setOpen(null);
              }}
            >
              New event
            </button>
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
          Show past events
        </label>
      </div>

      {events.length === 0 ? (
        <p className="empty">No events yet.</p>
      ) : (
        <ul className="card-list">
          {events.map((e) => (
            <li key={e.id}>
              <button className="row-card" onClick={() => setOpen(e)}>
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
                  {/* Visibility is moot once cancelled — the event is off the
                      calendar and off the website — so showing a green
                      "public" pill next to a red "cancelled" one just reads as
                      a contradiction. */}
                  {e.status !== 'cancelled' && e.visibility === 'public' && (
                    <span className="pill pill-ok">public</span>
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
          defaultTimezone={settings?.timezone}
          onClose={() => {
            setCreating(false);
            setOpen(null);
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

function EventDrawer({
  event,
  defaultTimezone,
  onClose,
  onChanged,
}: {
  event: EventRecord | null;
  defaultTimezone?: string;
  onClose: () => void;
  onChanged: (msg: string, next?: EventRecord) => void;
}) {
  const [form, setForm] = useState<EventInput>(() =>
    event
      ? {
          title: event.title,
          summary: event.summary ?? '',
          description: event.description ?? '',
          prep_notes: event.prep_notes ?? '',
          location: event.location ?? '',
          starts_at: toLocalInput(event.starts_at, event.timezone),
          ends_at: toLocalInput(event.ends_at, event.timezone),
          timezone: event.timezone,
          repeat_rule: event.rrule ?? '',
          visibility: event.visibility,
          venue: event.venue,
          space_impact: event.space_impact,
          expected_attendance: event.expected_attendance,
          registration_url: event.registration_url ?? '',
        }
      : { ...EMPTY_FORM, timezone: defaultTimezone },
  );
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const set = (patch: Partial<EventInput>) => setForm((f) => ({ ...f, ...patch }));

  const guard = async (fn: () => Promise<void>) => {
    setBusy(true);
    setErr(null);
    try {
      await fn();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const save = (ev: React.FormEvent) => {
    ev.preventDefault();
    return guard(async () => {
      if (!event) {
        const r = await api.createEvent(form);
        onChanged(
          'Created as a draft — it is not visible anywhere yet.',
          r.event,
        );
      } else {
        const r = await api.updateEvent(event.id, form);
        onChanged('Saved.', r.event);
      }
    });
  };

  const transition = (
    fn: (id: string) => Promise<{ event: EventRecord; warnings?: string[] | null }>,
    label: string,
  ) =>
    guard(async () => {
      if (!event) return;
      const r = await fn(event.id);
      const warnings = r.warnings?.length
        ? ` Worth knowing: ${r.warnings.join('; ')}.`
        : '';
      onChanged(`${label} — ${describeExposure(r.event).toLowerCase()}.${warnings}`, r.event);
    });

  return (
    <div className="drawer-backdrop" onClick={onClose}>
      <aside
        className="drawer drawer-wide"
        onClick={(e) => e.stopPropagation()}
      >
        <button className="drawer-close" onClick={onClose} aria-label="Close">
          ×
        </button>
        <h2 className="drawer-title">{event ? event.title : 'New event'}</h2>
        {event && <p className="muted">{describeExposure(event)}</p>}

        {err && <p className="banner banner-error">{err}</p>}

        <form onSubmit={save} className="stack-form">
          <label className="field">
            <span>Title</span>
            <input
              value={form.title ?? ''}
              onChange={(e) => set({ title: e.target.value })}
            />
          </label>

          <div className="field-row">
            <label className="field">
              <span>Starts</span>
              <input
                type="datetime-local"
                value={form.starts_at ?? ''}
                onChange={(e) => set({ starts_at: e.target.value })}
              />
            </label>
            <label className="field">
              <span>Ends</span>
              <input
                type="datetime-local"
                value={form.ends_at ?? ''}
                onChange={(e) => set({ ends_at: e.target.value })}
              />
            </label>
          </div>

          <label className="field">
            <span>Visibility</span>
            <select
              value={form.visibility ?? 'private'}
              onChange={(e) => set({ visibility: e.target.value })}
            >
              <option value="private">Private — internal only</option>
              <option value="public">Public — website and feed</option>
            </select>
            <span className="field-note">
              Public events appear on the website. Private ones stay on the team
              calendar.
            </span>
          </label>

          <label className="field">
            <span>Where</span>
            <select
              value={form.venue ?? 'onsite'}
              onChange={(e) => set({ venue: e.target.value })}
            >
              <option value="onsite">At our venue</option>
              <option value="offsite">Offsite — an event we attend</option>
            </select>
          </label>

          {form.venue !== 'offsite' && (
            <label className="field">
              <span>Space</span>
              <select
                value={form.space_impact ?? 'none'}
                onChange={(e) => set({ space_impact: e.target.value })}
              >
                <option value="none">Whole room open as usual</option>
                <option value="partial">Reserves part of the room</option>
              </select>
            </label>
          )}

          <label className="field">
            <span>Repeat</span>
            <select
              value={form.repeat_rule ? 'weekly' : 'none'}
              onChange={(e) =>
                set({
                  repeat_rule: e.target.value === 'weekly' ? 'FREQ=WEEKLY' : '',
                })
              }
            >
              <option value="none">Does not repeat</option>
              <option value="weekly">Every week</option>
            </select>
            <span className="field-note">
              For a standing weekly night like trivia. A run of different acts
              is not a repeat — create one event per night.
            </span>
          </label>

          <label className="field">
            <span>Summary</span>
            <input
              value={form.summary ?? ''}
              onChange={(e) => set({ summary: e.target.value })}
            />
          </label>

          <label className="field">
            <span>Public description</span>
            <textarea
              rows={4}
              value={form.description ?? ''}
              onChange={(e) => set({ description: e.target.value })}
            />
          </label>

          <label className="field">
            <span>Staff notes</span>
            <textarea
              rows={3}
              value={form.prep_notes ?? ''}
              onChange={(e) => set({ prep_notes: e.target.value })}
            />
            <span className="field-note">
              Goes on the calendar entry for whoever is working. Never appears
              on the website.
            </span>
          </label>

          <label className="field">
            <span>Location</span>
            <input
              value={form.location ?? ''}
              onChange={(e) => set({ location: e.target.value })}
            />
          </label>

          <label className="field">
            <span>Expected headcount</span>
            <input
              type="number"
              value={form.expected_attendance ?? ''}
              onChange={(e) =>
                set({
                  expected_attendance: e.target.value
                    ? Number(e.target.value)
                    : undefined,
                })
              }
            />
            <span className="field-note">
              What the food partner plans around.
            </span>
          </label>

          <label className="field">
            <span>Ticket or RSVP link</span>
            <input
              value={form.registration_url ?? ''}
              onChange={(e) => set({ registration_url: e.target.value })}
            />
          </label>

          {event && (
            <div className="field">
              <span>Poster</span>
              <span className="field-note">
                The graphic for this event. The website downloads it at build
                time and serves it from its own domain — nothing links back
                here.
              </span>
              {event.hero_attachment_id ? (
                <div className="poster-preview">
                  <img
                    src={api.eventPosterURL(event.id, event.updated_at)}
                    alt={`Poster for ${event.title}`}
                  />
                  <button
                    className="btn btn-ghost"
                    type="button"
                    disabled={busy}
                    onClick={() =>
                      guard(async () => {
                        const r = await api.deleteEventPoster(event.id);
                        onChanged('Poster removed.', r.event);
                      })
                    }
                  >
                    Remove poster
                  </button>
                </div>
              ) : (
                <input
                  type="file"
                  accept="image/jpeg,image/png,image/webp,image/gif"
                  disabled={busy}
                  onChange={(e) => {
                    const f = e.target.files?.[0];
                    if (!f) return;
                    // Reset first: picking the same file twice must re-fire.
                    e.target.value = '';
                    guard(async () => {
                      const r = await api.uploadEventPoster(event.id, f);
                      onChanged('Poster uploaded.', r.event);
                    });
                  }}
                />
              )}
            </div>
          )}

          <div className="drawer-actions">
            <button className="btn" type="submit" disabled={busy}>
              {busy ? 'Saving…' : event ? 'Save' : 'Create draft'}
            </button>
            {event?.status === 'draft' && (
              <button
                className="btn"
                type="button"
                disabled={busy}
                onClick={() => transition(api.publishEvent, 'Published')}
              >
                Publish
              </button>
            )}
            {event?.status === 'published' && (
              <>
                <button
                  className="btn btn-ghost"
                  type="button"
                  disabled={busy}
                  onClick={() => transition(api.unpublishEvent, 'Back to draft')}
                >
                  Unpublish
                </button>
                <button
                  className="btn btn-danger"
                  type="button"
                  disabled={busy}
                  onClick={() => transition(api.cancelEvent, 'Cancelled')}
                >
                  Cancel event
                </button>
              </>
            )}
            {event?.status === 'cancelled' && (
              <button
                className="btn btn-ghost"
                type="button"
                disabled={busy}
                onClick={() => transition(api.reopenEvent, 'Reopened')}
              >
                Reopen
              </button>
            )}
            {/* Erasing the row is only offered once it is no longer holding a
                pending calendar deletion; the server enforces that and says so
                if it is too early. */}
            {event && event.status !== 'published' && (
              <button
                className="btn btn-danger"
                type="button"
                disabled={busy}
                onClick={() =>
                  guard(async () => {
                    await api.deleteEvent(event.id);
                    onChanged(`Deleted "${event.title}" permanently.`);
                  })
                }
              >
                Delete permanently
              </button>
            )}
          </div>
        </form>
      </aside>
    </div>
  );
}
