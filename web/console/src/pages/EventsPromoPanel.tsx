import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type PromoItem, type PromoPayload } from '../api';

// The promotion work list, rendered on the Events page.
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

export default function EventsPromoPanel() {
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

  // Nothing configured yet is not an error state, and not worth a panel.
  if (!data || (data.channels.length === 0 && !err)) return null;

  const { items, done, summary } = data;

  return (
    <section className="panel">
      <div className="page-head-row">
        <h2 className="panel-title">
          Promotion
          {summary.outstanding > 0 && (
            <span className="btn-badge">{summary.outstanding}</span>
          )}
        </h2>
        <Link className="btn btn-ghost" to="/admin/events-channels">
          Channels
        </Link>
      </div>

      {err && <p className="banner banner-error">{err}</p>}

      {items.length === 0 ? (
        <p className="muted">Nothing outstanding. </p>
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

      {done.length > 0 && (
        <>
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
        </>
      )}
    </section>
  );
}
