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
}: {
  item: PromoItem;
  busy: boolean;
  onMark: (status: 'done' | 'ignored' | 'todo') => void;
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
        <strong>{item.event_title}</strong>
        <div className="muted">
          {item.channel_name} · {item.step_label || item.step_key}
        </div>
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

export default function EventsPromoPage() {
  const me = useMe();
  const [data, setData] = useState<PromoPayload | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [showDone, setShowDone] = useState(false);

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
        Where each event still needs posting, in the order it wants doing —
        soonest deadline first, not soonest event.
        {outstanding > 0 && overdue ? ` ${overdue} of ${outstanding} overdue.` : ''}
      </p>
    </div>
  );
}
