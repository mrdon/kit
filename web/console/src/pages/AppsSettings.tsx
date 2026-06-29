import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type WorkspaceApp } from '../api';
import { useRefreshMe } from '../me';
import { useSetChatContext } from '../chatContext';

// Admin-only page: turn feature apps on or off for the whole workspace.
// Disabling an app removes its tools, routes, cards, and agent prompt — not
// just its UI. Core apps (console, admin) are always on and not shown here.
export default function AppsSettings() {
  useSetChatContext('the admin Apps page (enable/disable features per workspace)');
  const refreshMe = useRefreshMe();
  const [rows, setRows] = useState<WorkspaceApp[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [toast, setToast] = useState<string | null>(null);
  const toastTimer = useRef<number | undefined>(undefined);

  useEffect(() => {
    api
      .apps()
      .then(setRows)
      .catch((e) => setErr(e.message));
  }, []);

  const flash = (msg: string) => {
    setToast(msg);
    window.clearTimeout(toastTimer.current);
    toastTimer.current = window.setTimeout(() => setToast(null), 3000);
  };
  useEffect(() => () => window.clearTimeout(toastTimer.current), []);

  const toggle = async (app: WorkspaceApp) => {
    const next = !app.enabled;
    setBusy(app.name);
    setErr(null);
    // Optimistic flip; revert on failure.
    setRows((rs) =>
      rs ? rs.map((r) => (r.name === app.name ? { ...r, enabled: next } : r)) : rs,
    );
    try {
      await api.setApp(app.name, next);
      flash(`${app.display_name} turned ${next ? 'on' : 'off'}.`);
      // Refresh shared /me so the top nav + launcher update live.
      refreshMe();
    } catch (e) {
      setErr((e as Error).message);
      setRows((rs) =>
        rs
          ? rs.map((r) => (r.name === app.name ? { ...r, enabled: app.enabled } : r))
          : rs,
      );
    } finally {
      setBusy(null);
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
          <span>Apps</span>
        </nav>
        <h1>Apps</h1>
        <p className="page-sub">
          Choose which features are available in this workspace. Turning an app
          off removes it everywhere — its tools, pages, cards, and agent
          knowledge — for everyone. Turn it back on at any time.
        </p>
      </div>

      {err && <p className="banner banner-error">{err}</p>}
      {toast && <p className="banner banner-ok">{toast}</p>}
      {!rows && !err && <p className="muted">Loading…</p>}

      <section className="card-list">
        {rows?.map((app) => (
          <article key={app.name} className="card">
            <div className="card-main">
              <span className="card-title">{app.display_name}</span>
              {app.description && (
                <span className="card-desc">{app.description}</span>
              )}
            </div>
            <div className="card-side">
              <label className="check">
                <input
                  type="checkbox"
                  checked={app.enabled}
                  disabled={busy === app.name}
                  onChange={() => toggle(app)}
                />
                {app.enabled ? 'On' : 'Off'}
              </label>
            </div>
          </article>
        ))}
      </section>
    </div>
  );
}
