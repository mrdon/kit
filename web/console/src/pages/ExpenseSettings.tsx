import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api } from '../api';

// Admin-only page (linked from the Admin area) for the workspace expense
// approval policy: which role approves reports. Server enforces admin access.
export default function ExpenseSettings() {
  const [roles, setRoles] = useState<string[]>([]);
  const [role, setRole] = useState('');
  const [err, setErr] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api.expensesMeta().then((m) => setRoles(m.roles)).catch(() => {});
    api
      .expensePolicy()
      .then((r) => setRole(r.policy.approver_role ?? ''))
      .catch((e) => setErr(e.message));
  }, []);

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    setSaved(false);
    try {
      await api.setExpensePolicy({ approver_role: role });
      setSaved(true);
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <Link to="/admin">Admin</Link>
          <span className="crumb-sep">/</span>
          <span>Expense settings</span>
        </nav>
        <h1>Expense settings</h1>
        <p className="page-sub">
          Choose which role approves expense reports. Only members of that role
          (and admins) can approve — and only they can see reports they didn't
          submit. Leave as “Admins only” to keep approvals with admins.
        </p>
      </div>

      {err && <p className="banner banner-error">{err}</p>}
      {saved && <p className="banner banner-ok">Saved.</p>}

      <form onSubmit={save} className="stack-form" style={{ maxWidth: '24rem' }}>
        <label className="field">
          <span>Approver role</span>
          <select value={role} onChange={(e) => setRole(e.target.value)}>
            <option value="">Admins only</option>
            {roles.map((r) => (
              <option key={r} value={r}>
                {r}
              </option>
            ))}
          </select>
        </label>
        <div className="drawer-actions">
          <button className="btn" type="submit" disabled={busy}>
            {busy ? 'Saving…' : 'Save'}
          </button>
        </div>
      </form>
    </div>
  );
}
