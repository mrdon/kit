import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type PromoItem, type PromoPayload } from '../api';
import { useMe } from '../me';

// The promotion work list. Its own page, deliberately.
//
// It started as a panel on the Events page and that was the wrong shape:
// Events is about authoring what is on, this is about chasing where it has
// been posted. They are different jobs done at different times -- one when a
// gig gets booked, the other in a single sitting once a week -- and stacking
// the second under the first buried the event list under a hundred rows of
// chores. Events links here with a count instead.
//
// One list in priority order, mixing states rather than separating a "todo"
// page from a log:
//
//   * outstanding work, deepest-link-first so it is paste-not-author
//   * what Kit did automatically, collapsed, so you can see it is working
//     without going to look
//   * what FAILED automatically, promoted back into the working list — a
//     channel that quietly stopped posting is worse than one that was never
//     automated, because the checklist stopped watching it too
//
// Ordering is by DUE date, not event date. A chamber wanting two weeks'
// notice is urgent for an event three weeks out while the Facebook post for
// the same event is not.
//
// Anything whose window has closed is already gone: the server never sends
// expired items. That is deliberate — a list that accumulates every reminder
// you missed is a guilt ledger, not a tool.

function dueLabel(item: PromoItem): string {
  const due = new Date(item.due_at);
  const days = Math.round((due.getTime() - Date.now()) / 86_400_000);
  if (days < 0) return `${Math.abs(days)}d overdue`;
  if (days === 0) return 'today';
  if (days === 1) return 'tomorrow';
  return `in ${days}d`;
}

function PromoRow({
  item,
  busy,
  onMark,
  // Inside an event group the heading already names the event, so repeating
  // it on all five rows is noise that crowds out the part that differs.
  hideEvent,
}: {
  item: PromoItem;
  busy: boolean;
  onMark: (status: 'done' | 'ignored' | 'todo') => void;
  hideEvent?: boolean;
}) {
  const failed = item.state === 'auto_failed';
  const settled = item.state === 'done' || item.state === 'auto_done';

  // Everything needed to do the job without hunting for it: the deep link,
  // the copy, and what was said last time.
  const copy = [
    item.event_title,
    new Date(item.event_start).toLocaleString(),
    item.url || '',
  ]
    .filter(Boolean)
    .join('\n');

  return (
    <tr className={item.overdue ? 'row-warn' : undefined}>
      <td>
        <input
          type="checkbox"
          checked={settled}
          disabled={busy}
          onChange={(e) => onMark(e.target.checked ? 'done' : 'todo')}
          aria-label={`Mark ${item.step_label} for ${item.event_title} done`}
        />
      </td>
      <td>
        {hideEvent ? (
          <strong>
            {item.channel_name} · {item.step_label || item.step_key}
          </strong>
        ) : (
          <>
            <strong>{item.event_title}</strong>
            <div className="muted">
              {item.channel_name} · {item.step_label || item.step_key}
            </div>
          </>
        )}
        {failed && (
          <div className="muted">
            ⚠ Kit tried to post this and failed{item.note ? `: ${item.note}` : '.'}{' '}
            Do it by hand, or fix the connection.
          </div>
        )}
        {item.step_kind === 'cadence' && item.last_done_at && (
          <div className="muted">
            Last posted {new Date(item.last_done_at).toLocaleDateString()}
            {item.last_url && (
              <>
                {' '}
                —{' '}
                <a href={item.last_url} target="_blank" rel="noreferrer">
                  see what you said
                </a>
              </>
            )}
          </div>
        )}
      </td>
      <td className="muted">{dueLabel(item)}</td>
      <td>
        {item.submit_url && (
          <a
            className="btn btn-ghost"
            href={item.submit_url}
            target="_blank"
            rel="noreferrer"
          >
            Open
          </a>
        )}
        <button
          className="btn btn-ghost"
          disabled={busy}
          onClick={() => navigator.clipboard?.writeText(copy)}
          title="Copy the title, date and link, ready to paste into their form"
        >
          Copy
        </button>
        <button
          className="btn btn-ghost"
          disabled={busy}
          onClick={() => onMark('ignored')}
          title="Not doing this one. It will not come back."
        >
          Skip
        </button>
      </td>
    </tr>
  );
}

// Two ways to read the same list, because there are two questions people
// bring to it.
//
//   event    — "where has everything got to?" Grouped and collapsed, so the
//              page opens as a short index of events with their counts rather
//              than a wall of rows. The default.
//   deadline — "what do I do next?" One flat run, soonest-due first, ignoring
//              which event each row belongs to.
//
// Collapsed-by-default is the point of the grouped view: a dozen events across
// four channels is over a hundred rows, and an index you expand one line at a
// time is the only version of that you can actually read.
type Grouping = 'deadline' | 'event';

// The choice is remembered because it reflects how someone works rather than
// what they are looking at right now, and re-picking it every visit would be
// its own small chore.
const GROUPING_KEY = 'kit.events.promo.grouping';

function loadGrouping(): Grouping {
  try {
    return localStorage.getItem(GROUPING_KEY) === 'deadline' ? 'deadline' : 'event';
  } catch {
    return 'event';
  }
}

// eventGroup is one event's outstanding work. `dueAt` is the soonest item in
// it, which is what the group sorts on -- so grouping reorders the page
// without abandoning urgency, and an event with something overdue still floats
// to the top.
type eventGroup = {
  eventID: string;
  title: string;
  items: PromoItem[];
  overdue: number;
  dueAt: string;
};

function groupByEvent(items: PromoItem[]): eventGroup[] {
  const byID = new Map<string, eventGroup>();
  for (const it of items) {
    let g = byID.get(it.event_id);
    if (!g) {
      g = { eventID: it.event_id, title: it.event_title, items: [], overdue: 0, dueAt: it.due_at };
      byID.set(it.event_id, g);
    }
    g.items.push(it);
    if (it.overdue) g.overdue++;
    if (it.due_at < g.dueAt) g.dueAt = it.due_at;
  }
  // Items arrive already sorted by urgency, so the groups only need ordering
  // against each other.
  return [...byID.values()].sort((a, b) => {
    if (a.overdue !== b.overdue) return b.overdue - a.overdue;
    return a.dueAt < b.dueAt ? -1 : a.dueAt > b.dueAt ? 1 : 0;
  });
}

export default function EventsPromoPage() {
  const me = useMe();
  const [data, setData] = useState<PromoPayload | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [showDone, setShowDone] = useState(false);
  const [grouping, setGrouping] = useState<Grouping>(loadGrouping);
  // Tracks what has been OPENED, so an untouched page is fully collapsed.
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  const chooseGrouping = (g: Grouping) => {
    setGrouping(g);
    try {
      localStorage.setItem(GROUPING_KEY, g);
    } catch {
      /* private browsing; the choice just will not stick */
    }
  };

  const toggleGroup = (id: string) =>
    setExpanded((prev) => {
      const next = new Set(prev);
      if (!next.delete(id)) next.add(id);
      return next;
    });

  useEffect(() => {
    api
      .eventsPromo()
      .then(setData)
      .catch((e) => setErr((e as Error).message));
  }, []);

  const mark = async (item: PromoItem, status: 'done' | 'ignored' | 'todo') => {
    setBusy(true);
    setErr(null);
    try {
      setData(
        await api.markEventPromo({
          event_id: item.event_id,
          channel_id: item.channel_id,
          step_key: item.step_key,
          status,
        }),
      );
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  if (!data && !err) return null;

  // An unconfigured workspace still gets a real page saying what this is for.
  // The first version rendered nothing at all, which made the whole feature
  // undiscoverable unless you already knew to go hunting in Admin.
  if (data && data.channels.length === 0) {
    return (
      <div className="page">
        <PromoHead outstanding={0} />
        <section className="panel">
          <p className="muted">
            Nothing set up yet. Kit can keep track of where each event still
            needs posting — your chamber, the city calendar, Facebook — and put
            the deep link and the copy in front of you rather than making you
            go and find them.
          </p>
          {me?.is_admin ? (
            <Link className="btn" to="/admin/events-channels">
              Set up promotion channels
            </Link>
          ) : (
            <p className="field-note">An admin can set the channels up.</p>
          )}
        </section>
      </div>
    );
  }
  if (!data) return null;

  const { items, done, summary } = data;

  return (
    <div className="page">
      <PromoHead outstanding={summary.outstanding} overdue={summary.overdue} />

      {err && <p className="banner banner-error">{err}</p>}

      <section className="panel">
        {items.length === 0 ? (
          <p className="muted">
            Nothing outstanding — everything due has been posted or skipped.
          </p>
        ) : (
          <>
            <div className="toolbar">
              <label className="check">
                <input
                  type="radio"
                  name="promo-grouping"
                  checked={grouping === 'event'}
                  onChange={() => chooseGrouping('event')}
                />
                By event
              </label>
              <label className="check">
                <input
                  type="radio"
                  name="promo-grouping"
                  checked={grouping === 'deadline'}
                  onChange={() => chooseGrouping('deadline')}
                />
                By deadline
              </label>
            </div>

            {grouping === 'deadline' ? (
              <table className="item-table">
                <tbody>
                  {items.map((it) => (
                    <PromoRow
                      key={`${it.event_id}:${it.channel_id}:${it.step_key}`}
                      item={it}
                      busy={busy}
                      onMark={(s) => mark(it, s)}
                    />
                  ))}
                </tbody>
              </table>
            ) : (
              groupByEvent(items).map((g) => (
                <EventGroup
                  key={g.eventID}
                  group={g}
                  open={expanded.has(g.eventID)}
                  busy={busy}
                  onToggle={() => toggleGroup(g.eventID)}
                  onMark={mark}
                />
              ))
            )}
          </>
        )}
      </section>

      {done.length > 0 && (
        <section className="panel">
          <button className="btn btn-ghost" onClick={() => setShowDone(!showDone)}>
            {showDone ? 'Hide' : 'Show'} {done.length} already done
          </button>
          {showDone && (
            <table className="item-table">
              <tbody>
                {done.map((it) => (
                  <PromoRow
                    key={`${it.event_id}:${it.channel_id}:${it.step_key}`}
                    item={it}
                    busy={busy}
                    onMark={(s) => mark(it, s)}
                  />
                ))}
              </tbody>
            </table>
          )}
        </section>
      )}
    </div>
  );
}

// PromoHead is the page chrome, shared by the empty and populated states so a
// workspace with nothing configured still lands somewhere that looks like a
// page rather than a stray paragraph.
function PromoHead({ outstanding, overdue }: { outstanding: number; overdue?: number }) {
  return (
    <div className="page-head">
      <nav className="crumbs">
        <Link to="/">Home</Link>
        <span className="crumb-sep">/</span>
        <Link to="/events">Events</Link>
        <span className="crumb-sep">/</span>
        <span>Promotion</span>
      </nav>
      <div className="page-head-row">
        <h1>Promotion</h1>
        <Link className="btn btn-ghost" to="/admin/events-channels">
          Channels
        </Link>
      </div>
      <p className="page-sub">
        Where each event still needs posting. Ordered by each destination&rsquo;s
        own deadline, not by when the event is.
        {outstanding > 0 && overdue ? ` ${overdue} of ${outstanding} overdue.` : ''}
      </p>
    </div>
  );
}

// EventGroup is one event's outstanding work under a collapsible heading.
//
// The heading carries the counts so a collapsed group still answers the
// question the grouped view exists for -- "where has this one got to?" --
// without being opened.
function EventGroup({
  group,
  open,
  busy,
  onToggle,
  onMark,
}: {
  group: eventGroup;
  open: boolean;
  busy: boolean;
  onToggle: () => void;
  onMark: (item: PromoItem, status: 'done' | 'ignored' | 'todo') => void;
}) {
  return (
    <div className="item-row">
      <button
        className="btn btn-ghost"
        onClick={onToggle}
        aria-expanded={open}
        title={open ? 'Collapse' : 'Expand'}
      >
        {open ? '▾' : '▸'} <strong>{group.title}</strong>{' '}
        <span className="muted">
          {group.items.length} outstanding
          {group.overdue > 0 ? ` · ${group.overdue} overdue` : ''}
        </span>
      </button>

      {open && (
        <table className="item-table">
          <tbody>
            {group.items.map((it) => (
              <PromoRow
                key={`${it.channel_id}:${it.step_key}`}
                item={it}
                busy={busy}
                hideEvent
                onMark={(s) => onMark(it, s)}
              />
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
