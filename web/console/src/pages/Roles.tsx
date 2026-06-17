import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type RolesMatrix } from '../api';

// Roles is the admin matrix of "who is in which role". Rows are users,
// columns are roles; toggling a cell assigns/unassigns. Membership changes
// apply optimistically and revert on error so the grid stays responsive.
export default function Roles() {
  const [data, setData] = useState<RolesMatrix | null>(null);
  const [err, setErr] = useState<string | null>(null);
  // Cells currently mid-request, keyed `${userId}:${roleName}` — disabled
  // so a double-click can't fire a second assign/unassign.
  const [pending, setPending] = useState<Set<string>>(new Set());

  const load = () =>
    api
      .roles()
      .then(setData)
      .catch((e) => setErr(e.message));

  useEffect(() => {
    void load();
  }, []);

  const toggle = async (
    slackUserID: string,
    userID: string,
    roleName: string,
    had: boolean,
  ) => {
    const key = `${userID}:${roleName}`;
    if (pending.has(key)) return;
    setErr(null);
    setPending((p) => new Set(p).add(key));
    // Optimistic update of the local matrix.
    setData((d) => d && applyMembership(d, userID, roleName, !had));
    try {
      if (had) await api.unassignRole(slackUserID, roleName);
      else await api.assignRole(slackUserID, roleName);
      // Refetch to pick up authoritative member counts.
      await load();
    } catch (e) {
      setErr((e as Error).message);
      // Revert the optimistic change.
      setData((d) => d && applyMembership(d, userID, roleName, had));
    } finally {
      setPending((p) => {
        const next = new Set(p);
        next.delete(key);
        return next;
      });
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
          <span>Roles</span>
        </nav>
        <h1>Roles</h1>
        <p className="page-sub">
          Who belongs to which role. Members of a role can see and edit every
          task that role owns. Toggle a cell to change membership.
        </p>
      </div>

      {err && <p className="banner banner-error">{err}</p>}
      {!data && !err && <p className="muted">Loading…</p>}

      {data && data.users.length === 0 && (
        <p className="muted">No users in this workspace yet.</p>
      )}

      {data && data.users.length > 0 && (
        <div className="roles-matrix-wrap">
          <table className="roles-matrix">
            <thead>
              <tr>
                <th className="roles-corner">User</th>
                {data.roles.map((r) => (
                  <th key={r.name} className="roles-col" title={r.description}>
                    <span className="roles-col-name">{r.name}</span>
                    <span className="roles-col-count">{r.member_count}</span>
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {data.users.map((u) => (
                <tr key={u.user_id}>
                  <th className="roles-user" scope="row">
                    <span className="roles-user-name">{u.display_name}</span>
                  </th>
                  {data.roles.map((r) => {
                    const had = u.roles.includes(r.name);
                    const key = `${u.user_id}:${r.name}`;
                    return (
                      <td key={r.name} className="roles-cell">
                        <input
                          type="checkbox"
                          checked={had}
                          disabled={pending.has(key)}
                          aria-label={`${u.display_name} in ${r.name}`}
                          onChange={() =>
                            toggle(u.slack_user_id, u.user_id, r.name, had)
                          }
                        />
                      </td>
                    );
                  })}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

// applyMembership returns a copy of the matrix with one user's membership of
// one role set to `member`. Pure so it works for both optimistic apply and
// revert.
function applyMembership(
  d: RolesMatrix,
  userID: string,
  roleName: string,
  member: boolean,
): RolesMatrix {
  return {
    roles: d.roles,
    users: d.users.map((u) => {
      if (u.user_id !== userID) return u;
      const has = u.roles.includes(roleName);
      if (member === has) return u;
      const roles = member
        ? [...u.roles, roleName].sort()
        : u.roles.filter((r) => r !== roleName);
      return { ...u, roles };
    }),
  };
}
