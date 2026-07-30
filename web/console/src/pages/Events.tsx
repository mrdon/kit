import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  api,
  type EventInput,
  type EventRecord,
  type EventsSettingsSummary,
} from '../api';
import { useSetChatContext } from '../chatContext';

// The everyday events page: list, create, edit, publish.
//
// The form and the chat launcher are two ways into the same service methods,
// not competing surfaces. A date picker beats typing "next Thursday", and chat
// beats a form for "move it to 7pm and make the blurb punchier" — so the page
// registers its chat context and refreshes when the agent changes something.

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

function describeExposure(e: EventRecord): string {
  if (e.status === 'draft') return 'not visible anywhere yet';
  if (e.status === 'cancelled') return 'removed from the calendar and website';
  if (e.visibility === 'public') return 'on the calendar and the public website';
  return 'on the team calendar only, not public';
}

function formatWhen(e: EventRecord): string {
  const d = new Date(e.starts_at);
  const opts: Intl.DateTimeFormatOptions = e.all_day
    ? { weekday: 'short', day: 'numeric', month: 'short', year: 'numeric' }
    : {
        weekday: 'short',
        day: 'numeric',
        month: 'short',
        hour: 'numeric',
        minute: '2-digit',
      };
  const base = d.toLocaleString(undefined, opts);
  return e.rrule ? `${base} · repeats weekly` : base;
}

export default function Events() {
  const [events, setEvents] = useState<EventRecord[]>([]);
  const [settings, setSettings] = useState<EventsSettingsSummary | null>(null);
  const [selected, setSelected] = useState<EventRecord | null>(null);
  const [form, setForm] = useState<EventInput>(EMPTY_FORM);
  const [creating, setCreating] = useState(false);
  const [includePast, setIncludePast] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(() => {
    api
      .listEvents({ include_past: includePast })
      .then((r) => {
        setEvents(r.events);
        setSettings(r.settings);
      })
      .catch((e) => setErr((e as Error).message));
  }, [includePast]);

  useEffect(load, [load]);

  // Page-aware chat: the agent resolves "this" and "move it to 7pm" against
  // whatever is open, and onTurnDone refreshes after it writes.
  useSetChatContext(
    selected
      ? `the Events page, viewing "${selected.title}"`
      : 'the Events page',
    load,
  );

  const edit = (e: EventRecord) => {
    setCreating(false);
    setSelected(e);
    setNote(null);
    setErr(null);
    setForm({
      title: e.title,
      summary: e.summary ?? '',
      description: e.description ?? '',
      prep_notes: e.prep_notes ?? '',
      location: e.location ?? '',
      starts_at: e.starts_at.slice(0, 16),
      ends_at: e.ends_at ? e.ends_at.slice(0, 16) : '',
      timezone: e.timezone,
      all_day: e.all_day,
      repeat_rule: e.rrule ?? '',
      visibility: e.visibility,
      venue: e.venue,
      space_impact: e.space_impact,
      capacity: e.capacity,
      expected_attendance: e.expected_attendance,
      price_cents: e.price_cents,
      registration_url: e.registration_url ?? '',
      notify_food_partner: e.notify_food_partner,
    });
  };

  const startNew = () => {
    setCreating(true);
    setSelected(null);
    setErr(null);
    setNote(null);
    setForm({ ...EMPTY_FORM, timezone: settings?.timezone });
  };

  const save = async () => {
    setBusy(true);
    setErr(null);
    setNote(null);
    try {
      if (creating) {
        const r = await api.createEvent(form);
        setNote('Created as a draft — it is not visible anywhere yet.');
        setCreating(false);
        setSelected(r.event);
      } else if (selected) {
        const r = await api.updateEvent(selected.id, form);
        setSelected(r.event);
        setNote('Saved.');
      }
      load();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const runTransition = async (
    fn: (id: string) => Promise<{ event: EventRecord; warnings?: string[] | null }>,
    label: string,
  ) => {
    if (!selected) return;
    setBusy(true);
    setErr(null);
    setNote(null);
    try {
      const r = await fn(selected.id);
      setSelected(r.event);
      const warnings = r.warnings?.length
        ? ` Worth knowing: ${r.warnings.join('; ')}.`
        : '';
      setNote(`${label} — ${describeExposure(r.event)}.${warnings}`);
      load();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const set = (patch: Partial<EventInput>) => setForm((f) => ({ ...f, ...patch }));

  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <span>Events</span>
        </nav>
        <h1>Events</h1>
        <p className="muted">
          Enter an event once. Published events sync to the team calendar;
          public ones also reach the website.
        </p>
      </div>

      {err && <div className="alert alert-error">{err}</div>}
      {note && <div className="alert">{note}</div>}
      {settings && !settings.calendar_configured && (
        <div className="alert">
          No calendar is selected yet, so nothing will sync.{' '}
          <Link to="/admin/events">Choose one in Events settings</Link>.
        </div>
      )}

      <div className="split">
        <section className="list-pane">
          <div className="inline-form">
            <button className="btn" onClick={startNew}>
              New event
            </button>
            <label className="check">
              <input
                type="checkbox"
                checked={includePast}
                onChange={(ev) => setIncludePast(ev.target.checked)}
              />
              Show past
            </label>
          </div>

          {events.length === 0 && <p className="muted">No events yet.</p>}
          <ul className="rows">
            {events.map((e) => (
              <li key={e.id}>
                <button
                  className={`row ${selected?.id === e.id ? 'row-active' : ''}`}
                  onClick={() => edit(e)}
                >
                  <span className="row-title">{e.title}</span>
                  <span className="muted">{formatWhen(e)}</span>
                  <span className="pills">
                    <span className={`pill pill-${e.status}`}>{e.status}</span>
                    {e.visibility === 'public' ? (
                      <span className="pill pill-ok">public</span>
                    ) : (
                      <span className="pill">private</span>
                    )}
                    {e.venue === 'offsite' && <span className="pill">offsite</span>}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        </section>

        <section className="detail-pane">
          {!creating && !selected && (
            <p className="muted">Pick an event, or create a new one.</p>
          )}

          {(creating || selected) && (
            <>
              <h2>{creating ? 'New event' : selected?.title}</h2>
              {selected && (
                <p className="muted">
                  {selected.status} — {describeExposure(selected)}
                </p>
              )}

              <label>
                Title
                <input
                  value={form.title ?? ''}
                  onChange={(e) => set({ title: e.target.value })}
                />
              </label>

              <div className="field-row">
                <label>
                  Starts
                  <input
                    type="datetime-local"
                    value={form.starts_at ?? ''}
                    onChange={(e) => set({ starts_at: e.target.value })}
                  />
                </label>
                <label>
                  Ends
                  <input
                    type="datetime-local"
                    value={form.ends_at ?? ''}
                    onChange={(e) => set({ ends_at: e.target.value })}
                  />
                </label>
              </div>

              <div className="field-row">
                <label>
                  Visibility
                  <select
                    value={form.visibility ?? 'private'}
                    onChange={(e) => set({ visibility: e.target.value })}
                  >
                    <option value="private">Private — internal only</option>
                    <option value="public">Public — website and feed</option>
                  </select>
                </label>
                <label>
                  Where
                  <select
                    value={form.venue ?? 'onsite'}
                    onChange={(e) => set({ venue: e.target.value })}
                  >
                    <option value="onsite">Onsite</option>
                    <option value="offsite">Offsite — an event we attend</option>
                  </select>
                </label>
              </div>

              {form.venue !== 'offsite' && (
                <label>
                  Space
                  <select
                    value={form.space_impact ?? 'none'}
                    onChange={(e) => set({ space_impact: e.target.value })}
                  >
                    <option value="none">Whole room open as usual</option>
                    <option value="partial">Reserves part of the room</option>
                  </select>
                </label>
              )}

              <label>
                Repeat
                <select
                  value={form.repeat_rule ? 'weekly' : 'none'}
                  onChange={(e) =>
                    set({
                      repeat_rule:
                        e.target.value === 'weekly' ? 'FREQ=WEEKLY' : '',
                    })
                  }
                >
                  <option value="none">Does not repeat</option>
                  <option value="weekly">Every week</option>
                </select>
                <span className="hint">
                  For a weekly night like trivia. A series of different acts is
                  not a repeat — create one event per night.
                </span>
              </label>

              <label>
                Summary
                <input
                  value={form.summary ?? ''}
                  onChange={(e) => set({ summary: e.target.value })}
                />
              </label>

              <label>
                Public description
                <textarea
                  rows={4}
                  value={form.description ?? ''}
                  onChange={(e) => set({ description: e.target.value })}
                />
              </label>

              <label>
                Staff notes
                <textarea
                  rows={3}
                  value={form.prep_notes ?? ''}
                  onChange={(e) => set({ prep_notes: e.target.value })}
                />
                <span className="hint">
                  Goes on the calendar entry for whoever is working. Never
                  appears on the website.
                </span>
              </label>

              <div className="field-row">
                <label>
                  Location
                  <input
                    value={form.location ?? ''}
                    onChange={(e) => set({ location: e.target.value })}
                  />
                </label>
                <label>
                  Expected headcount
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

              <label>
                Ticket or RSVP link
                <input
                  value={form.registration_url ?? ''}
                  onChange={(e) => set({ registration_url: e.target.value })}
                />
              </label>

              <div className="actions">
                <button className="btn" onClick={save} disabled={busy}>
                  {busy ? 'Saving…' : creating ? 'Create draft' : 'Save'}
                </button>
                {selected?.status === 'draft' && (
                  <button
                    className="btn"
                    disabled={busy}
                    onClick={() => runTransition(api.publishEvent, 'Published')}
                  >
                    Publish
                  </button>
                )}
                {selected?.status === 'published' && (
                  <>
                    <button
                      className="btn btn-secondary"
                      disabled={busy}
                      onClick={() =>
                        runTransition(api.unpublishEvent, 'Back to draft')
                      }
                    >
                      Unpublish
                    </button>
                    <button
                      className="btn btn-secondary"
                      disabled={busy}
                      onClick={() => runTransition(api.cancelEvent, 'Cancelled')}
                    >
                      Cancel event
                    </button>
                  </>
                )}
                {selected?.status === 'cancelled' && (
                  <button
                    className="btn btn-secondary"
                    disabled={busy}
                    onClick={() => runTransition(api.reopenEvent, 'Reopened')}
                  >
                    Reopen
                  </button>
                )}
              </div>
            </>
          )}
        </section>
      </div>
    </div>
  );
}
