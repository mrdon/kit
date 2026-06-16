import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type Integration } from '../api';

export default function Integrations() {
  const [rows, setRows] = useState<Integration[] | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    api
      .integrations()
      .then(setRows)
      .catch((e) => setErr(e.message));
  }, []);

  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <span>Integrations</span>
        </nav>
        <h1>Integrations</h1>
        <p className="page-sub">
          Connect the services Kit can act on for this workspace.
        </p>
      </div>

      {err && <p className="banner banner-error">{err}</p>}
      {!rows && !err && <p className="muted">Loading…</p>}

      <section className="card-list">
        {rows?.map((r) => (
          <article key={r.slug} className="card">
            <div className="card-main">
              <span className="card-title">{r.name}</span>
              <span className="card-desc">{r.description}</span>
            </div>
            <div className="card-side">
              {r.status_error ? (
                <span className="pill pill-error">Error</span>
              ) : r.connected ? (
                <span className="pill pill-ok">Connected</span>
              ) : (
                <span className="pill pill-off">Not connected</span>
              )}
              {r.detail && <span className="card-detail">{r.detail}</span>}
              {r.manage_url && (
                <a className="card-manage" href={r.manage_url}>
                  Manage
                </a>
              )}
            </div>
          </article>
        ))}
      </section>
    </div>
  );
}
