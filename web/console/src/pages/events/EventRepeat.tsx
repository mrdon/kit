// The repeat control: one place to say how often an event happens.
//
// Two mechanisms sit behind it, and the whole job of this component is to stop
// the person editing from having to know which is which. A RULE covers events
// that follow a pattern ("every Tuesday", "the first Friday"); a DATE LIST
// covers everything else, which in practice is most series -- dates picked
// around someone's availability, a run with a gap over a holiday.
//
// Rules are built from the start date rather than typed. Nobody should meet
// RFC 5545 to schedule a quiz night, and the server refuses a rule whose
// pattern does not include the start date anyway -- so offering only the
// patterns that DO match the chosen start turns a validation error into an
// option that was never there.

export type RepeatMode = 'none' | 'weekly' | 'monthly' | 'dates';

const WEEKDAY_CODES = ['SU', 'MO', 'TU', 'WE', 'TH', 'FR', 'SA'];
const WEEKDAY_LABELS = ['Su', 'Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa'];
const ORDINALS = ['', 'first', 'second', 'third', 'fourth', 'fifth'];

// Parses "2026-09-04T19:00" — the datetime-local wire format — as plain
// calendar fields. Deliberately NOT `new Date(value)`: that reads the string in
// the browser's zone, and the event's zone is usually the venue's, so a late
// evening event could land on the previous day and offer the wrong weekday.
function parseLocalInput(value: string | undefined): Date | null {
  if (!value) return null;
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})/.exec(value);
  if (!m) return null;
  return new Date(
    Number(m[1]),
    Number(m[2]) - 1,
    Number(m[3]),
    Number(m[4]),
    Number(m[5]),
  );
}

function ordinalSuffix(n: number): string {
  const mod100 = n % 100;
  if (mod100 >= 11 && mod100 <= 13) return `${n}th`;
  switch (n % 10) {
    case 1:
      return `${n}st`;
    case 2:
      return `${n}nd`;
    case 3:
      return `${n}rd`;
    default:
      return `${n}th`;
  }
}

function daysInMonth(d: Date): number {
  return new Date(d.getFullYear(), d.getMonth() + 1, 0).getDate();
}

export interface MonthlyOption {
  rule: string;
  label: string;
}

// monthlyOptions returns the monthly patterns that actually contain the given
// start date. "The last Friday" only appears when the start IS the last Friday
// of its month, which is what keeps every offered option valid on the server.
export function monthlyOptions(start: Date | null): MonthlyOption[] {
  if (!start) return [];
  const day = start.getDate();
  const code = WEEKDAY_CODES[start.getDay()];
  const weekdayName = start.toLocaleDateString(undefined, { weekday: 'long' });
  const ord = Math.ceil(day / 7);

  const out: MonthlyOption[] = [
    {
      rule: `FREQ=MONTHLY;BYDAY=${ord}${code}`,
      label: `Monthly on the ${ORDINALS[ord] ?? `${ord}th`} ${weekdayName}`,
    },
    {
      rule: `FREQ=MONTHLY;BYMONTHDAY=${day}`,
      label: `Monthly on the ${ordinalSuffix(day)}`,
    },
  ];
  if (day + 7 > daysInMonth(start)) {
    out.unshift({
      rule: `FREQ=MONTHLY;BYDAY=-1${code}`,
      label: `Monthly on the last ${weekdayName}`,
    });
  }
  return out;
}

// weeklyDays reads the selected weekdays out of a stored rule.
//
// A bare FREQ=WEEKLY has no BYDAY and means "the start date's own weekday",
// which is how every weekly event was stored before multi-day was offered. The
// start's day is always included whatever the rule says, because the server
// refuses a rule that does not contain it — so showing it unselected would be
// offering a state that cannot be saved.
export function weeklyDays(rule: string, start: Date | null): number[] {
  const set = new Set<number>();
  const m = /BYDAY=([A-Z,]+)/i.exec(rule);
  if (m) {
    for (const code of m[1].split(',')) {
      const i = WEEKDAY_CODES.indexOf(code.trim().toUpperCase());
      if (i >= 0) set.add(i);
    }
  }
  if (start) set.add(start.getDay());
  return [...set].sort((a, b) => a - b);
}

// buildWeekly renders a day set back to a rule.
//
// A set that is only the start's own weekday emits a bare FREQ=WEEKLY rather
// than BYDAY=WE. The two mean exactly the same thing, and opening an existing
// trivia night should not rewrite its rule into a longer spelling of itself --
// that would show up as a pending website change for an event nobody edited.
export function buildWeekly(days: number[], start: Date | null): string {
  const set = new Set(days);
  if (start) set.add(start.getDay());
  const sorted = [...set].sort((a, b) => a - b);
  if (start && sorted.length === 1 && sorted[0] === start.getDay()) {
    return 'FREQ=WEEKLY';
  }
  return `FREQ=WEEKLY;BYDAY=${sorted.map((d) => WEEKDAY_CODES[d]).join(',')}`;
}

// modeOf infers which control to show from what is already stored, so
// reopening an event lands on the mode it was saved in.
export function modeOf(rule: string, dates: string[]): RepeatMode {
  if (dates.length > 0) return 'dates';
  if (!rule) return 'none';
  return /FREQ=MONTHLY/i.test(rule) ? 'monthly' : 'weekly';
}

export default function RepeatEditor({
  startsAt,
  rule,
  dates,
  onChange,
  disabled,
}: {
  startsAt: string | undefined;
  rule: string;
  dates: string[];
  onChange: (next: { repeat_rule: string; repeat_dates: string[] }) => void;
  disabled?: boolean;
}) {
  const start = parseLocalInput(startsAt);
  const mode = modeOf(rule, dates);
  const months = monthlyOptions(start);
  const selectedDays = weeklyDays(rule, start);

  // A new row is pre-filled a week on from the last date at the same time,
  // because an empty date picker is a worse starting point than a wrong one
  // that is one click from right. Blank rows are ignored by the server, so a
  // suggestion never becomes an accidental date.
  const nextDateSuggestion = (): string => {
    const last = parseLocalInput(dates[dates.length - 1]) ?? start;
    if (!last) return '';
    const d = new Date(last.getTime());
    d.setDate(d.getDate() + 7);
    const pad = (n: number) => String(n).padStart(2, '0');
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(
      d.getHours(),
    )}:${pad(d.getMinutes())}`;
  };

  // Switching mode always clears the other mechanism. Carrying a stale rule
  // under a date list would quietly union the two on the calendar -- the server
  // supports that combination, but nobody picking "on set dates" means it.
  const setMode = (next: RepeatMode) => {
    switch (next) {
      case 'none':
        onChange({ repeat_rule: '', repeat_dates: [] });
        break;
      case 'weekly':
        onChange({ repeat_rule: 'FREQ=WEEKLY', repeat_dates: [] });
        break;
      case 'monthly':
        onChange({ repeat_rule: months[0]?.rule ?? '', repeat_dates: [] });
        break;
      case 'dates':
        onChange({ repeat_rule: '', repeat_dates: [nextDateSuggestion()] });
        break;
    }
  };

  // Clicking the start date's own day is a no-op rather than an error: the
  // server requires the rule to contain it, so there is nothing to turn off.
  const toggleWeekday = (day: number) => {
    if (start && day === start.getDay()) return;
    const next = selectedDays.includes(day)
      ? selectedDays.filter((d) => d !== day)
      : [...selectedDays, day];
    onChange({ repeat_rule: buildWeekly(next, start), repeat_dates: [] });
  };

  const setDate = (i: number, value: string) => {
    const next = dates.slice();
    next[i] = value;
    onChange({ repeat_rule: '', repeat_dates: next });
  };

  const removeDate = (i: number) => {
    const next = dates.filter((_, j) => j !== i);
    onChange({ repeat_rule: '', repeat_dates: next });
  };

  return (
    <div className="field">
      <span>Repeat</span>
      <select
        value={mode}
        disabled={disabled}
        onChange={(e) => setMode(e.target.value as RepeatMode)}
      >
        <option value="none">Does not repeat</option>
        <option value="weekly">Every week</option>
        <option value="monthly" disabled={!start}>
          Every month
        </option>
        <option value="dates">On set dates</option>
      </select>

      {mode === 'weekly' && (
        <>
          <div className="seg">
            {WEEKDAY_LABELS.map((label, i) => {
              const on = selectedDays.includes(i);
              const isStart = start != null && i === start.getDay();
              return (
                <button
                  key={WEEKDAY_CODES[i]}
                  type="button"
                  className={`seg-btn${on ? ' seg-active' : ''}`}
                  disabled={disabled}
                  aria-pressed={on}
                  title={
                    isStart
                      ? 'The event starts on this day, so it is always included'
                      : undefined
                  }
                  onClick={() => toggleWeekday(i)}
                >
                  {label}
                </button>
              );
            })}
          </div>
          <span className="field-note">
            For a standing weekly night like trivia, or an offer that runs
            several days — a Sunday-to-Tuesday pizza deal is one event on three
            weekdays, not three events. The start date's own day is always
            included.
          </span>
        </>
      )}

      {mode === 'monthly' && (
        <>
          <select
            value={rule}
            disabled={disabled}
            onChange={(e) =>
              onChange({ repeat_rule: e.target.value, repeat_dates: [] })
            }
          >
            {months.map((o) => (
              <option key={o.rule} value={o.rule}>
                {o.label}
              </option>
            ))}
          </select>
          <span className="field-note">
            A month that has no such date is skipped rather than moved — a
            series on the 31st simply does not run in February.
          </span>
        </>
      )}

      {mode === 'dates' && (
        <>
          <span className="field-note">
            One event on several dates: one web page, one poster, one set of
            staff notes. The start above is the first date; add the rest here.
          </span>
          <ul className="date-list">
            {dates.map((d, i) => (
              <li key={i}>
                <input
                  type="datetime-local"
                  value={d}
                  disabled={disabled}
                  onChange={(e) => setDate(i, e.target.value)}
                />
                <button
                  type="button"
                  className="btn btn-ghost"
                  disabled={disabled}
                  onClick={() => removeDate(i)}
                  aria-label={`Remove date ${i + 2}`}
                >
                  Remove
                </button>
              </li>
            ))}
          </ul>
          <button
            type="button"
            className="btn btn-ghost"
            disabled={disabled}
            onClick={() =>
              onChange({
                repeat_rule: '',
                repeat_dates: [...dates, nextDateSuggestion()],
              })
            }
          >
            Add a date
          </button>
        </>
      )}
    </div>
  );
}
