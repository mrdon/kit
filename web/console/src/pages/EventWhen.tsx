// The start/end picker.
//
// Nearly every event here runs on ONE day for about two hours, and the default
// pair of full datetime-local inputs made you restate the date twice to say so.
// This asks for the date once, then two times, and keeps the full second date
// available for the genuine exception — a two-day festival.
//
// Times move in 15-minute steps and default to the hour, because a taproom
// event has never started at 7:07pm and offering that precision only adds
// scrolling.

const STEP_SECONDS = 15 * 60;

// The default length of a new event. The model would fall back to an hour if
// the end were left empty, but two hours is the honest common case here and a
// value on screen is easier to correct than an invisible assumption.
const DEFAULT_DURATION_MINUTES = 120;

const pad = (n: number) => String(n).padStart(2, '0');

// Renders the wire format used by <input type="datetime-local"> and accepted
// by the API: a naive local wall clock, no zone. The server reads it in the
// EVENT's timezone, which is the whole reason this must not go through
// toISOString() -- that would convert to UTC and silently move the event.
export function toLocalValue(d: Date): string {
  return (
    `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}` +
    `T${pad(d.getHours())}:${pad(d.getMinutes())}`
  );
}

function parseLocalValue(v: string | undefined): Date | null {
  if (!v) return null;
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})/.exec(v);
  if (!m) return null;
  return new Date(+m[1], +m[2] - 1, +m[3], +m[4], +m[5]);
}

const datePart = (v: string | undefined) => (v ? v.slice(0, 10) : '');
const timePart = (v: string | undefined) => (v ? v.slice(11, 16) : '');

// The default start for a new event: the next whole hour. Rounding up rather
// than down because an event is being scheduled, not logged -- a default in the
// past is never the answer.
export function defaultStart(): string {
  const d = new Date();
  d.setMinutes(0, 0, 0);
  d.setHours(d.getHours() + 1);
  return toLocalValue(d);
}

export function addMinutes(v: string, minutes: number): string {
  const d = parseLocalValue(v);
  if (!d) return '';
  d.setMinutes(d.getMinutes() + minutes);
  return toLocalValue(d);
}

// True when the two ends fall on different calendar days, which is what puts
// the control into its expanded form on reopening a festival.
export function spansDays(startsAt?: string, endsAt?: string): boolean {
  if (!startsAt || !endsAt) return false;
  return datePart(startsAt) !== datePart(endsAt);
}

export default function EventWhen({
  startsAt,
  endsAt,
  multiDay,
  onMultiDay,
  onChange,
  disabled,
}: {
  startsAt?: string;
  endsAt?: string;
  multiDay: boolean;
  onMultiDay: (v: boolean) => void;
  onChange: (next: { starts_at?: string; ends_at?: string }) => void;
  disabled?: boolean;
}) {
  // Same-day and the end reads as earlier than the start. Worth saying out
  // loud: the server refuses it, and the fix (a night running past midnight is
  // a two-day event) is not obvious from the error alone.
  const endsBeforeStart =
    !multiDay &&
    !!startsAt &&
    !!endsAt &&
    datePart(startsAt) === datePart(endsAt) &&
    timePart(endsAt) < timePart(startsAt);

  // Moving the start carries the end with it, so the pair stays the same length
  // instead of the end being left behind on the old date.
  const setStart = (value: string) => {
    if (!value) {
      onChange({ starts_at: value });
      return;
    }
    if (!endsAt) {
      onChange({
        starts_at: value,
        ends_at: addMinutes(value, DEFAULT_DURATION_MINUTES),
      });
      return;
    }
    if (multiDay) {
      const prev = parseLocalValue(startsAt);
      const next = parseLocalValue(value);
      const end = parseLocalValue(endsAt);
      if (prev && next && end) {
        const shifted = new Date(end.getTime() + (next.getTime() - prev.getTime()));
        onChange({ starts_at: value, ends_at: toLocalValue(shifted) });
        return;
      }
      onChange({ starts_at: value });
      return;
    }
    // Same-day: the end keeps its time and simply follows the new date.
    onChange({ starts_at: value, ends_at: `${datePart(value)}T${timePart(endsAt)}` });
  };

  // In same-day mode the end input holds only a time; its date is always the
  // start's, which is what removes the second date field entirely.
  const setEndTime = (time: string) => {
    if (!time) {
      onChange({ ends_at: '' });
      return;
    }
    onChange({ ends_at: `${datePart(startsAt) || datePart(endsAt)}T${time}` });
  };

  const toggleMultiDay = (on: boolean) => {
    onMultiDay(on);
    // Leaving a multi-day event collapses the end back onto the start's date,
    // so the control never claims "same day" while holding a later one.
    if (!on && startsAt && endsAt) {
      onChange({ ends_at: `${datePart(startsAt)}T${timePart(endsAt)}` });
    }
  };

  return (
    <>
      <div className="field-row">
        <label className="field">
          <span>Starts</span>
          <input
            type="datetime-local"
            step={STEP_SECONDS}
            value={startsAt ?? ''}
            disabled={disabled}
            onChange={(e) => setStart(e.target.value)}
          />
        </label>
        <label className="field">
          <span>{multiDay ? 'Ends' : 'Until'}</span>
          {multiDay ? (
            <input
              type="datetime-local"
              step={STEP_SECONDS}
              value={endsAt ?? ''}
              disabled={disabled}
              onChange={(e) => onChange({ ends_at: e.target.value })}
            />
          ) : (
            <input
              type="time"
              step={STEP_SECONDS}
              value={timePart(endsAt)}
              disabled={disabled}
              onChange={(e) => setEndTime(e.target.value)}
            />
          )}
        </label>
      </div>

      <label className="check">
        <input
          type="checkbox"
          checked={multiDay}
          disabled={disabled}
          onChange={(e) => toggleMultiDay(e.target.checked)}
        />
        Ends on a different day
      </label>

      {endsBeforeStart && (
        <span className="field-hint">
          This ends before it starts. For a night that runs past midnight, tick
          “Ends on a different day”.
        </span>
      )}
    </>
  );
}
