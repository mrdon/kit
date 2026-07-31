import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  api,
  type EventInput,
  type EventRecord,
  type EventsSettingsSummary,
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

  const load = useCallback(() => {
    api
      .listEvents({ include_past: includePast })
      .then((r) => {
        setEvents(r.events ?? []);
        setSettings(r.settings);
      })
      .catch((e) => setErr((e as Error).message));
  }, [includePast]);

  useEffect(load, [load]);

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
          No calendar is selected, so nothing will sync.{' '}
          <Link to="/admin/events">Choose one</Link>.
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
                  <span className="row-card-sub">{formatWhen(e)}</span>
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
                  {e.visibility === 'public' && (
                    <span className="pill pill-ok">public</span>
                  )}
                  {e.venue === 'offsite' && <span className="pill">offsite</span>}
                </span>
              </button>
            </li>
          ))}
        </ul>
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
          starts_at: event.starts_at.slice(0, 16),
          ends_at: event.ends_at ? event.ends_at.slice(0, 16) : '',
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

          <div className="field-row">
            <label className="field">
              <span>Where</span>
              <select
                value={form.venue ?? 'onsite'}
                onChange={(e) => set({ venue: e.target.value })}
              >
                <option value="onsite">Onsite</option>
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
          </div>

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

          <div className="field-row">
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
            </label>
          </div>

          <label className="field">
            <span>Ticket or RSVP link</span>
            <input
              value={form.registration_url ?? ''}
              onChange={(e) => set({ registration_url: e.target.value })}
            />
          </label>

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
          </div>
        </form>
      </aside>
    </div>
  );
}
