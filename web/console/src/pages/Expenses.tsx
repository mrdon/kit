import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  api,
  type CreateExpenseBody,
  type ExpenseFilters,
  type ExpenseReport,
  type ExpensesMeta,
} from '../api';
import ExpenseDetail from './expenses/detail';
import { STATUS_LABEL, STATUSES, formatCents } from './expenses/common';

// Status order for the grouped view: actionable first.
const GROUP_ORDER = ['submitted', 'draft', 'rejected', 'approved', 'reimbursed'];

export default function Expenses() {
  const [meta, setMeta] = useState<ExpensesMeta | null>(null);
  const [reports, setReports] = useState<ExpenseReport[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [filters, setFilters] = useState<ExpenseFilters>({ include_closed: true });
  const [openId, setOpenId] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);

  useEffect(() => {
    api.expensesMeta().then(setMeta).catch(() => {});
  }, []);

  const load = useCallback(() => {
    api
      .listExpenses(filters)
      .then((r) => setReports(r.reports))
      .catch((e) => setErr(e.message));
  }, [filters]);
  useEffect(load, [load]);

  const setFilter = (k: keyof ExpenseFilters, v: string | boolean) =>
    setFilters((f) => ({ ...f, [k]: v === '' ? undefined : v }));

  const groups = GROUP_ORDER.map((status) => ({
    status,
    items: reports.filter((r) => r.status === status),
  })).filter((g) => g.items.length > 0);

  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <span>Expenses</span>
        </nav>
        <div className="page-head-row">
          <h1>Expenses</h1>
          <div className="page-head-actions">
            <button className="btn" onClick={() => setCreating(true)}>
              New report
            </button>
          </div>
        </div>
      </div>

      {err && <p className="banner banner-error">{err}</p>}

      <div className="toolbar">
        <select onChange={(e) => setFilter('status', e.target.value)} defaultValue="">
          <option value="">Any status</option>
          {STATUSES.map((s) => (
            <option key={s} value={s}>
              {STATUS_LABEL[s]}
            </option>
          ))}
        </select>
        <label className="check">
          <input
            type="checkbox"
            onChange={(e) => setFilter('mine_only', e.target.checked)}
          />
          Mine only
        </label>
      </div>

      {groups.length === 0 ? (
        <p className="empty">No expense reports yet.</p>
      ) : (
        groups.map((g) => (
          <section key={g.status} className="group">
            <h2 className="group-title">
              {STATUS_LABEL[g.status]} <span className="group-count">{g.items.length}</span>
            </h2>
            <ul className="card-list">
              {g.items.map((r) => (
                <li key={r.id}>
                  <button className="row-card" onClick={() => setOpenId(r.id)}>
                    <span className="row-card-main">
                      <span className="row-card-title">{r.title}</span>
                      {r.rejection_reason && (
                        <span className="row-card-sub">Rejected: {r.rejection_reason}</span>
                      )}
                    </span>
                    <span className="row-card-amount">{formatCents(r.total_cents, r.currency)}</span>
                  </button>
                </li>
              ))}
            </ul>
          </section>
        ))
      )}

      {openId && (
        <ExpenseDetail
          reportId={openId}
          onClose={() => setOpenId(null)}
          onChanged={load}
        />
      )}

      {creating && (
        <CreateExpense
          meta={meta}
          onClose={() => setCreating(false)}
          onCreated={() => {
            setCreating(false);
            load();
          }}
        />
      )}
    </div>
  );
}

function CreateExpense({
  meta,
  onClose,
  onCreated,
}: {
  meta: ExpensesMeta | null;
  onClose: () => void;
  onCreated: () => void;
}) {
  const [body, setBody] = useState<CreateExpenseBody>({ title: '', currency: 'USD' });
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const set = (k: keyof CreateExpenseBody, v: string) =>
    setBody((b) => ({ ...b, [k]: v }));

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    try {
      await api.createExpense(body);
      onCreated();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="drawer-backdrop" onClick={onClose}>
      <aside className="drawer" onClick={(e) => e.stopPropagation()}>
        <button className="drawer-close" onClick={onClose} aria-label="Close">
          ×
        </button>
        <h2 className="panel-title">New expense report</h2>
        {err && <p className="banner banner-error">{err}</p>}
        <form onSubmit={submit} className="stack-form">
          <label className="field">
            <span>Title</span>
            <input required autoFocus onChange={(e) => set('title', e.target.value)} />
          </label>
          <div className="field-row">
            <label className="field">
              <span>Currency</span>
              <select value={body.currency} onChange={(e) => set('currency', e.target.value)}>
                {(meta?.currencies ?? ['USD']).map((c) => (
                  <option key={c} value={c}>
                    {c}
                  </option>
                ))}
              </select>
            </label>
            <label className="field">
              <span>Role</span>
              <select onChange={(e) => set('role_scope', e.target.value)} defaultValue="">
                <option value="">Primary role</option>
                {meta?.roles.map((r) => (
                  <option key={r} value={r}>
                    {r}
                  </option>
                ))}
              </select>
            </label>
          </div>
          <label className="field">
            <span>Approver (optional)</span>
            <input
              placeholder="name, @slack id, or UUID — blank = anyone in the role"
              onChange={(e) => set('approver', e.target.value)}
            />
          </label>
          <label className="field">
            <span>Description</span>
            <textarea rows={3} onChange={(e) => set('description', e.target.value)} />
          </label>
          <div className="drawer-actions">
            <button className="btn" type="submit" disabled={busy}>
              {busy ? 'Creating…' : 'Create report'}
            </button>
          </div>
        </form>
      </aside>
    </div>
  );
}
