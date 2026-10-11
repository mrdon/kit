import type { EventInput, EventRecord } from '../../api';

export const EMPTY_FORM: EventInput = {
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
  repeat_dates: [],
  registration_url: '',
  prominence: 'normal',
  labels: [],
};

// The API returns an instant in UTC; <input type="datetime-local"> holds a
// naive wall-clock, and the server parses what it gets back in the EVENT'S
// timezone. So the value has to be rendered in that zone -- slicing the ISO
// string instead shows the UTC wall time, and saving any other field then
// writes that wrong time back and silently moves the event. That is a data
// bug, not a display one.
export function toLocalInput(iso: string | undefined, tz: string | undefined): string {
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
export const verbFor = (action: string) => VERBS[action] ?? 'changed';

// Spelled out because "published" is routinely misread as "public". They are
// separate axes: a confirmed private booking is published AND private.
export function describeExposure(e: EventRecord): string {
  if (e.status === 'draft') return 'Not visible anywhere yet';
  if (e.status === 'cancelled') return 'Removed from the calendar and website';
  if (e.visibility === 'public') return 'On the calendar and the public website';
  return 'On the team calendar only, not public';
}

// A short gloss of a stored rule for the list line. Deliberately coarse — the
// drawer shows the exact pattern, this only has to say "there is more than one
// of these" at a glance.
export function describeRule(rule: string): string {
  if (!/FREQ=MONTHLY/i.test(rule)) return 'repeats weekly';
  const ord = /BYDAY=(-?\d)([A-Z]{2})/i.exec(rule);
  if (ord) {
    const names: Record<string, string> = {
      SU: 'Sunday', MO: 'Monday', TU: 'Tuesday', WE: 'Wednesday',
      TH: 'Thursday', FR: 'Friday', SA: 'Saturday',
    };
    const which = ord[1] === '-1' ? 'last' : ['', 'first', 'second', 'third', 'fourth', 'fifth'][Number(ord[1])];
    return `monthly, ${which} ${names[ord[2].toUpperCase()] ?? ''}`.trim();
  }
  return 'repeats monthly';
}

export function formatWhen(e: EventRecord): string {
  // The NEXT date, not the first. starts_at is the first occurrence, so for a
  // weekly quiz that began in 2024 or a class whose series started in August it
  // is months behind — and the list is read to find out what is coming up.
  // Falls back to starts_at once every date has passed, so a finished series
  // still shows when it last ran rather than nothing at all.
  const d = new Date(e.next_occurrence ?? e.starts_at);
  const base = d.toLocaleString(undefined, {
    // The event's own zone, not the reader's: a manager checking the roster
    // from another state still needs the time the doors actually open.
    timeZone: e.timezone || undefined,
    weekday: 'short',
    day: 'numeric',
    month: 'short',
    ...(e.all_day ? {} : { hour: 'numeric', minute: '2-digit' }),
  });
  if (e.rrule) return `${base} · ${describeRule(e.rrule)}`;
  const total = e.date_count ?? 0;
  return total > 0 ? `${base} · ${total} dates` : base;
}
