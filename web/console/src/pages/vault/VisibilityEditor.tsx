import { useState } from 'react';
import { vaultApi, type Principal } from './api';
import { lock, unlock } from './worker';

const roleLabel = (name?: string) =>
  !name || name === 'member' ? 'Everyone (members)' : name;

// VisibilityEditor re-scopes an existing entry to another role. Re-scoping
// has its own endpoint (PUT /entries/:id/role), separate from the field
// edit, so the audit trail stays clean. The server enforces who may pick
// which role (members of the target role, or any admin) and, for a
// cross-role move, requires a recent unlock (step-up). When the step-up
// window has lapsed the PUT returns 401; we then prompt for the master
// password inline, refresh the unlock, and retry rather than bouncing the
// user to login.
export function VisibilityEditor({
  entryId,
  roleId,
  roleName,
  onChanged,
}: {
  entryId: string;
  roleId?: string;
  roleName?: string;
  onChanged: () => void;
}) {
  const [editing, setEditing] = useState(false);
  const [roles, setRoles] = useState<Principal[]>([]);
  const [selected, setSelected] = useState(roleId || '');
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // Non-null means "awaiting master password to satisfy step-up".
  const [stepUpPw, setStepUpPw] = useState<string | null>(null);

  const begin = async () => {
    setErr(null);
    setStepUpPw(null);
    setEditing(true);
    try {
      const d = await vaultApi<{ roles: Principal[]; default_role_id?: string }>('GET', '/principals');
      let rs = d.roles || [];
      // Keep the current role selectable even if it's not in the caller's
      // member-filtered list (e.g. owner who has since left that role).
      if (roleId && !rs.some((r) => r.id === roleId)) {
        rs = [{ id: roleId, name: roleName || 'current role' }, ...rs];
      }
      setRoles(rs);
      setSelected(roleId || d.default_role_id || '');
    } catch (e) {
      setErr((e as Error).message);
    }
  };

  const save = async () => {
    setBusy(true);
    setErr(null);
    try {
      await vaultApi('PUT', `/entries/${entryId}/role`, { role_id: selected }, { noAuthRedirect: true });
      setEditing(false);
      setStepUpPw(null);
      onChanged();
    } catch (e) {
      const msg = String((e as Error).message);
      if (msg.includes('HTTP 403')) {
        setErr("You can only move this to a role you're in (admins can pick any role).");
      } else if (msg.includes('HTTP 401')) {
        setStepUpPw('');
      } else {
        setErr(msg);
      }
    } finally {
      setBusy(false);
    }
  };

  const confirmStepUp = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    try {
      // unlock() no-ops while the key is held, so drop it first to force a
      // fresh /unlock that re-stamps the step-up window.
      await lock();
      await unlock(stepUpPw || '');
    } catch (e) {
      setErr((e as Error).message);
      setBusy(false);
      return;
    }
    setStepUpPw(null);
    await save();
  };

  if (!editing) {
    return (
      <div className="entry-visibility">
        <span className="muted">Who can see this: {roleLabel(roleName)}</span>
        <button className="btn btn-ghost" onClick={begin}>
          Change
        </button>
      </div>
    );
  }

  if (stepUpPw !== null) {
    return (
      <form onSubmit={confirmStepUp} className="stack-form">
        <p className="muted">Re-enter the master password to change who can see this.</p>
        {err && <p className="banner banner-error">{err}</p>}
        <label className="field">
          <span>Master password</span>
          <input
            type="password"
            autoFocus
            autoComplete="current-password"
            value={stepUpPw}
            onChange={(e) => setStepUpPw(e.target.value)}
          />
        </label>
        <div className="drawer-actions">
          <button className="btn" type="submit" disabled={busy}>
            {busy ? 'Confirming…' : 'Confirm change'}
          </button>
          <button
            className="btn btn-ghost"
            type="button"
            disabled={busy}
            onClick={() => {
              setStepUpPw(null);
              setEditing(false);
            }}
          >
            Cancel
          </button>
        </div>
      </form>
    );
  }

  return (
    <div className="stack-form">
      {err && <p className="banner banner-error">{err}</p>}
      <label className="field">
        <span>Who can see it</span>
        <select value={selected} onChange={(e) => setSelected(e.target.value)}>
          {roles.map((r) => (
            <option key={r.id} value={r.id}>
              {roleLabel(r.name)}
            </option>
          ))}
        </select>
      </label>
      <div className="drawer-actions">
        <button className="btn" type="button" disabled={busy || !selected} onClick={save}>
          {busy ? 'Saving…' : 'Save'}
        </button>
        <button className="btn btn-ghost" type="button" disabled={busy} onClick={() => setEditing(false)}>
          Cancel
        </button>
      </div>
    </div>
  );
}
