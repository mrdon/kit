import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type RoleInfo, type RolesMatrix, type RoleTarget } from '../api';
import { useSetChatContext } from '../chatContext';

// Roles is the admin matrix of "who is in which role". Rows are users, then
// paired devices; columns are roles; toggling a cell assigns/unassigns.
// Membership changes apply optimistically and revert on error so the grid
// stays responsive.
export default function Roles() {
  useSetChatContext('the admin Roles page (which people and devices are in which role)');
  const [data, setData] = useState<RolesMatrix | null>(null);
  const [err, setErr] = useState<string | null>(null);
  // Cells currently mid-request, keyed `${rowKey}:${roleName}` — disabled
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

  const toggle = async (row: Row, roleName: string, had: boolean) => {
    const key = `${row.key}:${roleName}`;
    if (pending.has(key)) return;
    setErr(null);
    setPending((p) => new Set(p).add(key));
    // Optimistic update of the local matrix.
    setData((d) => d && applyMembership(d, row, roleName, !had));
    try {
      if (had) await api.unassignRole(row.target, roleName);
      else await api.assignRole(row.target, roleName);
      // Refetch to pick up authoritative member counts.
      await load();
    } catch (e) {
      setErr((e as Error).message);
      // Revert the optimistic change.
      setData((d) => d && applyMembership(d, row, roleName, had));
    } finally {
      setPending((p) => {
        const next = new Set(p);
        next.delete(key);
        return next;
      });
    }
  };

  const users = data ? data.users.map(userRow) : [];
  const devices = data ? (data.devices ?? []).map(deviceRow) : [];

  const renderRow = (row: Row) => (
    <tr key={row.key}>
      <th className="roles-user" scope="row">
        <span className="roles-user-name">{row.name}</span>
      </th>
      {data!.roles.map((r) => {
        const had = row.roles.includes(r.name);
        const key = `${row.key}:${r.name}`;
        const lock = lockReason(row, r);
        return (
          <td key={r.name} className="roles-cell">
            <input
              type="checkbox"
              checked={had || (row.kind === 'user' && r.catchall)}
              disabled={pending.has(key) || lock !== null}
              aria-label={`${row.name} in ${r.name}`}
              title={lock ?? undefined}
              onChange={() => toggle(row, r.name, had)}
            />
          </td>
        );
      })}
    </tr>
  );

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
          task that role owns. Paired devices see only what the roles ticked
          for them allow, and can't be admins. Toggle a cell to change
          membership.
        </p>
      </div>

      {err && <p className="banner banner-error">{err}</p>}
      {!data && !err && <p className="muted">Loading…</p>}

      {data && users.length === 0 && devices.length === 0 && (
        <p className="muted">No users in this workspace yet.</p>
      )}

      {data && (users.length > 0 || devices.length > 0) && (
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
              {users.map(renderRow)}
              {devices.length > 0 && (
                <tr>
                  <th className="roles-section" colSpan={data.roles.length + 1} scope="rowgroup">
                    Devices
                  </th>
                </tr>
              )}
              {devices.map(renderRow)}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

// Row is one line of the matrix, a person or a paired device.
interface Row {
  key: string;
  kind: 'user' | 'device';
  name: string;
  roles: string[];
  target: RoleTarget;
}

// Key on Slack id — workspace members without a Kit record yet have no
// user_id, but the Slack id is always present and unique.
const userRow = (u: RolesMatrix['users'][number]): Row => ({
  key: `u:${u.slack_user_id}`,
  kind: 'user',
  name: u.display_name,
  roles: u.roles,
  target: { slack_user_id: u.slack_user_id },
});

const deviceRow = (d: RolesMatrix['devices'][number]): Row => ({
  key: `d:${d.actor_id}`,
  kind: 'device',
  name: d.label,
  roles: d.roles,
  target: { actor_id: d.actor_id },
});

// lockReason says why a cell can't be toggled, or null if it can. Member is
// a catchall for people only; a device never holds admin.
function lockReason(row: Row, role: RoleInfo): string | null {
  if (row.kind === 'user' && role.catchall) return 'Everyone is a member';
  if (row.kind === 'device' && role.name === 'admin') return "A device can't be an admin";
  return null;
}

// applyMembership returns a copy of the matrix with one row's membership of
// one role set to `member`. Pure so it works for both optimistic apply and
// revert.
function applyMembership(
  d: RolesMatrix,
  row: Row,
  roleName: string,
  member: boolean,
): RolesMatrix {
  const set = (roles: string[]) => {
    const has = roles.includes(roleName);
    if (member === has) return roles;
    return member ? [...roles, roleName].sort() : roles.filter((r) => r !== roleName);
  };
  return {
    roles: d.roles,
    users: d.users.map((u) =>
      row.kind === 'user' && `u:${u.slack_user_id}` === row.key ? { ...u, roles: set(u.roles) } : u,
    ),
    devices: (d.devices ?? []).map((x) =>
      row.kind === 'device' && `d:${x.actor_id}` === row.key ? { ...x, roles: set(x.roles) } : x,
    ),
  };
}
