import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api } from '../api';
import { SLUG } from '../workspace';
import { useSetChatContext } from '../chatContext';

const CURRENCIES = ['USD', 'EUR', 'GBP', 'CAD', 'AUD'];

// Admin-only page (linked from the Admin area) for the workspace expense
// approval policy and the public intake page. Server enforces admin access.
export default function ExpenseSettings() {
  useSetChatContext('the admin Expense Settings page (approval policy + intake)');
  const [roles, setRoles] = useState<string[]>([]);
  const [role, setRole] = useState('');
  const [err, setErr] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const [busy, setBusy] = useState(false);

  // Public intake config.
  const [intakeEnabled, setIntakeEnabled] = useState(false);
  const [intakeRole, setIntakeRole] = useState('');
  const [intakeCurrency, setIntakeCurrency] = useState('USD');
  const [intakeErr, setIntakeErr] = useState<string | null>(null);
  const [intakeSaved, setIntakeSaved] = useState(false);
  const [intakeBusy, setIntakeBusy] = useState(false);

  useEffect(() => {
    api.expensesMeta().then((m) => setRoles(m.roles)).catch(() => {});
    api
      .expensePolicy()
      .then((r) => {
        setRole(r.policy.approver_role ?? '');
        setIntakeEnabled(r.policy.intake_enabled);
        setIntakeRole(r.policy.intake_role ?? '');
        setIntakeCurrency(r.policy.intake_currency || 'USD');
      })
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

  const saveIntake = async (e: React.FormEvent) => {
    e.preventDefault();
    setIntakeBusy(true);
    setIntakeErr(null);
    setIntakeSaved(false);
    try {
      await api.setExpenseIntake({
        enabled: intakeEnabled,
        role: intakeRole,
        currency: intakeCurrency,
      });
      setIntakeSaved(true);
    } catch (e) {
      setIntakeErr((e as Error).message);
    } finally {
      setIntakeBusy(false);
    }
  };

  const intakeURL = `${location.origin}/${SLUG}/expenses/submit`;

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

      <div className="page-head" style={{ marginTop: '2rem' }}>
        <h2>Public receipt intake</h2>
        <p className="page-sub">
          A public page where anyone — including people without a Slack account
          — can upload a receipt and submit an expense. Submissions land with
          the role you pick and route through the approval policy above. No
          money moves until someone approves it.
        </p>
      </div>

      {intakeErr && <p className="banner banner-error">{intakeErr}</p>}
      {intakeSaved && <p className="banner banner-ok">Saved.</p>}

      <form
        onSubmit={saveIntake}
        className="stack-form"
        style={{ maxWidth: '24rem' }}
      >
        <label className="check">
          <input
            type="checkbox"
            checked={intakeEnabled}
            onChange={(e) => setIntakeEnabled(e.target.checked)}
          />
          Enable the public intake page
        </label>
        <label className="field">
          <span>Submissions owned &amp; approved by</span>
          <select
            value={intakeRole}
            onChange={(e) => setIntakeRole(e.target.value)}
          >
            <option value="">Choose a role…</option>
            {roles.map((r) => (
              <option key={r} value={r}>
                {r}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          <span>Default currency</span>
          <select
            value={intakeCurrency}
            onChange={(e) => setIntakeCurrency(e.target.value)}
          >
            {CURRENCIES.map((c) => (
              <option key={c} value={c}>
                {c}
              </option>
            ))}
          </select>
        </label>
        <div className="drawer-actions">
          <button className="btn" type="submit" disabled={intakeBusy}>
            {intakeBusy ? 'Saving…' : 'Save'}
          </button>
        </div>
      </form>

      {intakeEnabled && (
        <p className="page-sub" style={{ maxWidth: '32rem' }}>
          Share this link:{' '}
          <a href={intakeURL} target="_blank" rel="noreferrer">
            {intakeURL}
          </a>
        </p>
      )}
    </div>
  );
}
