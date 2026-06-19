import { useCallback, useEffect, useState } from 'react';
import {
  api,
  type ExpenseEvent,
  type ExpenseItem,
  type ExpenseItemBody,
  type ExpenseReport,
} from '../../api';
import { STATUS_LABEL, formatCents } from './common';

export default function ExpenseDetail({
  reportId,
  onClose,
  onChanged,
}: {
  reportId: string;
  onClose: () => void;
  onChanged: () => void;
}) {
  const [report, setReport] = useState<ExpenseReport | null>(null);
  const [events, setEvents] = useState<ExpenseEvent[]>([]);
  const [canApprove, setCanApprove] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(() => {
    api
      .getExpense(reportId)
      .then((r) => {
        setReport(r.report);
        setEvents(r.events);
        setCanApprove(r.can_approve);
      })
      .catch((e) => setErr(e.message));
  }, [reportId]);
  useEffect(load, [load]);

  // run wraps an action: clears errors, reloads this drawer + the parent list,
  // and surfaces any failure inline.
  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    setErr(null);
    try {
      await fn();
      load();
      onChanged();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  // del removes the report and closes the drawer (reloading it would 404).
  const del = async () => {
    if (!window.confirm('Delete this report? This cannot be undone.')) return;
    setBusy(true);
    setErr(null);
    try {
      await api.deleteExpense(reportId);
      onChanged();
      onClose();
    } catch (e) {
      setErr((e as Error).message);
      setBusy(false);
    }
  };

  if (!report) {
    return (
      <div className="drawer-backdrop" onClick={onClose}>
        <aside className="drawer" onClick={(e) => e.stopPropagation()}>
          <button className="drawer-close" onClick={onClose} aria-label="Close">
            ×
          </button>
          {err ? <p className="banner banner-error">{err}</p> : <p>Loading…</p>}
        </aside>
      </div>
    );
  }

  const isDraft = report.status === 'draft';
  const items = report.items ?? [];

  return (
    <div className="drawer-backdrop" onClick={onClose}>
      <aside className="drawer drawer-wide" onClick={(e) => e.stopPropagation()}>
        <button className="drawer-close" onClick={onClose} aria-label="Close">
          ×
        </button>
        <h2 className="panel-title">{report.title}</h2>
        <p className="detail-meta">
          <span className={`status-pill status-${report.status}`}>
            {STATUS_LABEL[report.status]}
          </span>
          <span className="detail-total">{formatCents(report.total_cents, report.currency)}</span>
        </p>
        {report.description && <p className="detail-desc">{report.description}</p>}
        {report.rejection_reason && (
          <p className="banner banner-error">Rejected: {report.rejection_reason}</p>
        )}
        {err && <p className="banner banner-error">{err}</p>}

        <ItemTable
          items={items}
          currency={report.currency}
          editable={isDraft}
          onRemove={(itemId) => run(() => api.removeExpenseItem(reportId, itemId))}
        />

        {isDraft && (
          <AddItemForm busy={busy} onAdd={(body) => run(() => api.addExpenseItem(reportId, body))} />
        )}

        <Actions report={report} canApprove={canApprove} busy={busy} run={run} onDelete={del} />

        {events.length > 0 && (
          <div className="activity">
            <h3 className="activity-title">Activity</h3>
            <ul className="activity-list">
              {events.map((e) => (
                <li key={e.id}>
                  <span className="activity-when">
                    {new Date(e.created_at).toLocaleString()}
                  </span>
                  <span>{describeEvent(e)}</span>
                </li>
              ))}
            </ul>
          </div>
        )}
      </aside>
    </div>
  );
}

function ItemTable({
  items,
  currency,
  editable,
  onRemove,
}: {
  items: ExpenseItem[];
  currency: string;
  editable: boolean;
  onRemove: (itemId: string) => void;
}) {
  if (items.length === 0) {
    return <p className="empty">No line items yet.</p>;
  }
  return (
    <table className="item-table">
      <thead>
        <tr>
          <th>Vendor</th>
          <th>Date</th>
          <th>Category</th>
          <th className="num">Amount</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        {items.map((it) => (
          <tr key={it.id}>
            <td>
              {it.vendor || <span className="muted">—</span>}
              {it.attachment_id && <span title="Receipt attached"> 📎</span>}
            </td>
            <td>{it.spent_on ? it.spent_on.slice(0, 10) : '—'}</td>
            <td>{it.category || '—'}</td>
            <td className="num">{formatCents(it.amount_cents, currency)}</td>
            <td>
              {editable && (
                <button
                  className="link-danger"
                  onClick={() => onRemove(it.id)}
                  aria-label="Remove item"
                >
                  ×
                </button>
              )}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function AddItemForm({
  busy,
  onAdd,
}: {
  busy: boolean;
  onAdd: (body: ExpenseItemBody) => void;
}) {
  const [body, setBody] = useState<ExpenseItemBody>({});
  const set = (k: keyof ExpenseItemBody, v: string) => setBody((b) => ({ ...b, [k]: v }));

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!body.amount) return;
    onAdd(body);
    setBody({});
  };

  return (
    <form onSubmit={submit} className="add-item">
      <input
        placeholder="Vendor"
        value={body.vendor ?? ''}
        onChange={(e) => set('vendor', e.target.value)}
      />
      <input type="date" value={body.spent_on ?? ''} onChange={(e) => set('spent_on', e.target.value)} />
      <input
        className="num"
        placeholder="0.00"
        inputMode="decimal"
        value={body.amount ?? ''}
        onChange={(e) => set('amount', e.target.value)}
      />
      <button className="btn btn-sm" type="submit" disabled={busy || !body.amount}>
        Add
      </button>
    </form>
  );
}

function Actions({
  report,
  canApprove,
  busy,
  run,
  onDelete,
}: {
  report: ExpenseReport;
  canApprove: boolean;
  busy: boolean;
  run: (fn: () => Promise<unknown>) => void;
  onDelete: () => void;
}) {
  const id = report.id;
  switch (report.status) {
    case 'draft':
      return (
        <div className="drawer-actions">
          <button
            className="btn"
            disabled={busy || (report.items ?? []).length === 0}
            onClick={() => run(() => api.submitExpense(id))}
          >
            Submit for approval
          </button>
          <button className="btn btn-ghost btn-danger" disabled={busy} onClick={onDelete}>
            Delete
          </button>
        </div>
      );
    case 'submitted':
      // Only an eligible approver sees the controls; the submitter just waits.
      if (!canApprove) {
        return <p className="detail-desc">Submitted — awaiting approval.</p>;
      }
      return (
        <div className="drawer-actions">
          <button className="btn" disabled={busy} onClick={() => run(() => api.approveExpense(id))}>
            Approve
          </button>
          <button
            className="btn btn-ghost"
            disabled={busy}
            onClick={() => {
              const reason = window.prompt('Reason for rejection?') ?? '';
              run(() => api.rejectExpense(id, reason));
            }}
          >
            Reject
          </button>
        </div>
      );
    case 'approved':
      return (
        <div className="drawer-actions">
          <button className="btn" disabled={busy} onClick={() => run(() => api.reimburseExpense(id))}>
            Mark reimbursed
          </button>
        </div>
      );
    case 'rejected':
      return (
        <div className="drawer-actions">
          <button className="btn" disabled={busy} onClick={() => run(() => api.reopenExpense(id))}>
            Reopen to fix
          </button>
          <button className="btn btn-ghost btn-danger" disabled={busy} onClick={onDelete}>
            Delete
          </button>
        </div>
      );
    default:
      return null;
  }
}

function describeEvent(e: ExpenseEvent): string {
  switch (e.event_type) {
    case 'comment':
      return e.content || 'Comment';
    case 'item_added':
      return `Added ${e.content}`;
    case 'item_removed':
      return `Removed ${e.content}`;
    case 'submitted':
      return 'Submitted for approval';
    case 'approved':
      return 'Approved';
    case 'rejected':
      return `Rejected${e.content ? `: ${e.content}` : ''}`;
    case 'reimbursed':
      return 'Marked reimbursed';
    case 'status_change':
      return `${e.old_value} → ${e.new_value}${e.content ? ` (${e.content})` : ''}`;
    default:
      return e.event_type;
  }
}
