import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type Integration, type IntegrationType } from '../api';
import { useSetChatContext } from '../chatContext';

export default function Integrations() {
  useSetChatContext('the Integrations page (connecting external services)');
  const [catalog, setCatalog] = useState<IntegrationType[] | null>(null);
  const [cards, setCards] = useState<Integration[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const load = () => {
    api
      .integrationCatalog()
      .then(setCatalog)
      .catch((e) => setErr(e.message));
    // Custom-flow integrations keep their own cards; only
    // admins can see/manage them, and the endpoint 403s for non-admins.
    api
      .me()
      .then((me) => {
        if (me.is_admin) api.integrations().then(setCards).catch(() => {});
      })
      .catch(() => {});
  };
  useEffect(load, []);

  const connect = async (t: IntegrationType) => {
    setErr(null);
    setBusy(true);
    try {
      const { url } = await api.integrationConnect(t.provider, t.auth_type);
      window.location.href = url;
    } catch (e) {
      setErr((e as Error).message);
      setBusy(false);
    }
  };

  const remove = async (t: IntegrationType) => {
    if (!window.confirm(`Disconnect ${t.display_name}?`)) return;
    setErr(null);
    setBusy(true);
    try {
      await api.integrationDelete(t.integration_id);
      load();
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
          <span>Integrations</span>
        </nav>
        <h1>Integrations</h1>
        <p className="page-sub">
          Connect the services Kit can act on. Secrets are entered on a secure
          one-time page and never pass through the assistant.
        </p>
      </div>

      {err && <p className="banner banner-error">{err}</p>}
      {!catalog && !err && <p className="muted">Loading…</p>}

      <section className="card-list">
        {catalog?.map((t) => (
          <article key={`${t.provider}:${t.auth_type}`} className="card">
            <div className="card-main">
              <span className="card-title">
                {t.display_name}
                {t.scope === 'user' ? ' (personal)' : ''}
              </span>
              <span className="card-desc">{t.description}</span>
            </div>
            <div className="card-side">
              {t.connected ? (
                <span className="pill pill-ok">Connected</span>
              ) : (
                <span className="pill pill-off">Not connected</span>
              )}
              {t.can_manage ? (
                t.connected ? (
                  <>
                    <button className="btn" onClick={() => connect(t)} disabled={busy}>
                      Reconnect
                    </button>
                    <button
                      className="btn btn-danger"
                      onClick={() => remove(t)}
                      disabled={busy}
                    >
                      Disconnect
                    </button>
                  </>
                ) : (
                  <button className="btn" onClick={() => connect(t)} disabled={busy}>
                    Connect
                  </button>
                )
              ) : (
                !t.connected && <span className="card-detail">Admin only</span>
              )}
            </div>
          </article>
        ))}

        {cards.map((r) => (
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
