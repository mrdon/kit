import { useState } from 'react';
import {
  api,
  STANDARD_LABELS,
  type EventInput,
  type EventRecord,
} from '../../api';
import RepeatEditor from './EventRepeat';
import EventWhen, { defaultStart, addMinutes, spansDays } from './EventWhen';
import EventPosterPicker from '../posters/EventPosterPicker';
import { EMPTY_FORM, toLocalInput, describeExposure } from './format';

export default function EventDrawer({
  event,
  postersOn,
  defaultTimezone,
  onClose,
  onChanged,
}: {
  event: EventRecord | null;
  postersOn: boolean;
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
          repeat_dates: (event.rdates ?? []).map((d) =>
            toLocalInput(d, event.timezone),
          ),
          visibility: event.visibility,
          venue: event.venue,
          space_impact: event.space_impact,
          expected_attendance: event.expected_attendance,
          registration_url: event.registration_url ?? '',
          prominence: event.prominence,
          labels: event.labels ?? [],
        }
      : (() => {
          const starts = defaultStart();
          return {
            ...EMPTY_FORM,
            timezone: defaultTimezone,
            starts_at: starts,
            ends_at: addMinutes(starts, 120),
          };
        })(),
  );
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // Derived from the event on open rather than stored: a festival reopens with
  // the two-date form, everything else with the compact one.
  const [multiDay, setMultiDay] = useState(() =>
    spansDays(
      toLocalInput(event?.starts_at, event?.timezone),
      toLocalInput(event?.ends_at, event?.timezone),
    ),
  );

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

          <EventWhen
            startsAt={form.starts_at}
            endsAt={form.ends_at}
            multiDay={multiDay}
            onMultiDay={setMultiDay}
            disabled={busy}
            onChange={(next) => set(next)}
          />

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
            <span>Prominence</span>
            <select
              value={form.prominence ?? 'normal'}
              onChange={(e) =>
                set({ prominence: e.target.value as EventInput['prominence'] })
              }
            >
              <option value="featured">Featured — the website leads with it</option>
              <option value="normal">Normal — a regular event</option>
              <option value="background">Background — a standing offer</option>
            </select>
          </label>
          <span className="field-note">
            Most events are Normal: they headline their own day on the printed
            table topper. Featured is for the one the website should lead with —
            several can be marked at once, and it shows the next one. Background
            is for a standing offer like a weekly pizza deal or happy hour: it
            still gets printed and published, but never takes the headline off a
            real event on the same day.
          </span>

          {/* Labels are open-ended, so the presets are checkboxes rather than
              the only option: tick the usual ones, type anything else. Typing
              is what keeps "wednesday-market" possible; the presets are what
              stop "charity" and "give-back" becoming three labels that the
              website's Give Back page matches none of. */}
          <fieldset className="field">
            <legend>Labels</legend>
            <div className="label-presets">
              {STANDARD_LABELS.map(({ label, means }) => (
                <label key={label} className="label-preset" title={means}>
                  <input
                    type="checkbox"
                    checked={(form.labels ?? []).includes(label)}
                    onChange={(e) => {
                      const current = form.labels ?? [];
                      set({
                        labels: e.target.checked
                          ? [...current, label]
                          : current.filter((l) => l !== label),
                      });
                    }}
                  />
                  <span>{label}</span>
                </label>
              ))}
            </div>
            <input
              type="text"
              placeholder="Anything else, comma separated"
              value={(form.labels ?? [])
                .filter((l) => !STANDARD_LABELS.some((s) => s.label === l))
                .join(', ')}
              onChange={(e) => {
                const custom = e.target.value
                  .split(',')
                  .map((l) => l.trim())
                  .filter(Boolean);
                const standard = (form.labels ?? []).filter((l) =>
                  STANDARD_LABELS.some((s) => s.label === l),
                );
                set({ labels: [...standard, ...custom] });
              }}
            />
            <span className="field-note">
              What kind of thing this is, so the website can group events onto
              their own page. Not how loudly it speaks — that is Prominence.
              Reuse a label that already exists rather than inventing a synonym
              for it: the website matches the exact word, so a second spelling
              quietly splits the group in two. Saved lowercased and hyphenated.
            </span>
          </fieldset>

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

          <RepeatEditor
            startsAt={form.starts_at}
            rule={form.repeat_rule ?? ''}
            dates={form.repeat_dates ?? []}
            disabled={busy}
            onChange={(next) => set(next)}
          />

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
              {/* Generated options first; the manual upload below stays for a
                  poster made elsewhere. Only once the event exists: a new
                  event saves as a draft first. */}
              {postersOn && <EventPosterPicker eventId={event.id} onChanged={onChanged} />}
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
            {/* Offered whatever the status: copying a cancelled event is how
                you reinstate one without reusing its web address, and copying
                a published one is the everyday "same again next month". */}
            {event && (
              <button
                className="btn btn-ghost"
                type="button"
                disabled={busy}
                onClick={() =>
                  guard(async () => {
                    const r = await api.cloneEvent(event.id);
                    onChanged(
                      'Copied as a new draft. Set its date and publish when ready.',
                      r.event,
                    );
                  })
                }
              >
                Duplicate
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
