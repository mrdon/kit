import { useEffect, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { api, type NetlifyStatus } from '../api';
import { useSetChatContext } from '../chatContext';

export default function Netlify() {
  useSetChatContext('the admin Website (Netlify) page');
  const [st, setSt] = useState<NetlifyStatus | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [siteId, setSiteId] = useState('');
  const [busy, setBusy] = useState(false);
  const [params, setParams] = useSearchParams();
  const banner = params.get('msg');

  const load = () => {
    api
      .netlifyStatus()
      .then(setSt)
      .catch((e) => setErr(e.message));
  };
  useEffect(load, []);

  const dismissBanner = () => {
    params.delete('msg');
    setParams(params, { replace: true });
  };

  const pickSite = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!siteId) return;
    setBusy(true);
    setErr(null);
    try {
      await api.netlifyPickSite(siteId);
      load();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const disconnect = async () => {
    setBusy(true);
    setErr(null);
    try {
      await api.netlifyDisconnect();
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
          <Link to="/admin">Admin</Link>
          <span className="crumb-sep">/</span>
          <span>Website</span>
        </nav>
        <h1>Website settings</h1>
        <p className="page-sub">
          Connect this workspace to Netlify and GitHub so the team can request
          website changes from Slack.
        </p>
      </div>

      {banner && (
        <p className="banner banner-ok" onClick={dismissBanner}>
          {banner}
        </p>
      )}
      {err && <p className="banner banner-error">{err}</p>}
      {!st && !err && <p className="muted">Loading…</p>}

      {st && (
        <>
          <section className="panel">
            <h2 className="panel-title">Netlify</h2>
            {st.netlify_connected ? (
              <>
                <p className="status-line">
                  <span className="pill pill-ok">Connected</span> Site:{' '}
                  <code>{st.netlify_site_name}</code>
                </p>
                <button className="btn btn-danger" onClick={disconnect} disabled={busy}>
                  Disconnect Netlify
                </button>
              </>
            ) : st.netlify_needs_picker ? (
              <>
                <p className="status-line">
                  <span className="pill pill-ok">Signed in</span> Pick which
                  Netlify site this workspace will edit.
                </p>
                {st.netlify_sites_error ? (
                  <p className="muted">{st.netlify_sites_error}</p>
                ) : st.sites_by_team.length === 0 ? (
                  <p className="muted">
                    No Netlify sites visible to your account. Create one, then
                    refresh.
                  </p>
                ) : (
                  <form className="inline-form" onSubmit={pickSite}>
                    <select
                      required
                      value={siteId}
                      onChange={(e) => setSiteId(e.target.value)}
                    >
                      <option value="" disabled>
                        Choose a site…
                      </option>
                      {st.sites_by_team.map((g) => (
                        <optgroup key={g.team} label={g.team}>
                          {g.sites.map((s) => (
                            <option key={s.id} value={s.id}>
                              {s.name}
                              {s.url ? ` — ${s.url}` : ''}
                            </option>
                          ))}
                        </optgroup>
                      ))}
                    </select>
                    <button className="btn" type="submit" disabled={busy}>
                      Use this site
                    </button>
                  </form>
                )}
                <button
                  className="btn btn-danger btn-spaced"
                  onClick={disconnect}
                  disabled={busy}
                >
                  Cancel and disconnect
                </button>
              </>
            ) : !st.netlify_configured ? (
              <p className="status-line">
                <span className="pill pill-off">Unavailable</span> The Netlify
                OAuth app hasn't been configured for this Kit install.
              </p>
            ) : (
              <>
                <p className="status-line">
                  <span className="pill pill-off">Not connected</span> Sign in
                  with Netlify to pick which site this workspace edits.
                </p>
                <a className="btn" href={st.netlify_connect_url}>
                  Connect Netlify
                </a>
              </>
            )}
          </section>

          <section className="panel">
            <h2 className="panel-title">GitHub</h2>
            {st.github_connected ? (
              <>
                <p className="status-line">
                  <span className="pill pill-ok">Connected</span>{' '}
                  {st.github_account_login
                    ? `Installed on ${st.github_account_login}.`
                    : `Installation #${st.github_installation_id}.`}
                  {st.netlify_repo_owner && st.netlify_repo_name
                    ? ` Will edit ${st.netlify_repo_owner}/${st.netlify_repo_name}.`
                    : ''}
                </p>
                <a className="btn btn-danger" href={st.github_disconnect_url}>
                  Disconnect GitHub
                </a>
              </>
            ) : !st.github_configured ? (
              <p className="status-line">
                <span className="pill pill-off">Unavailable</span> The Kit
                GitHub App hasn't been configured for this Kit install.
              </p>
            ) : (
              <>
                <p className="status-line">
                  <span className="pill pill-off">Not connected</span> Install
                  the Kit GitHub App on the repo that backs your Netlify site
                  {st.netlify_repo_owner && st.netlify_repo_name
                    ? ` (${st.netlify_repo_owner}/${st.netlify_repo_name})`
                    : ''}
                  .
                </p>
                <a className="btn" href={st.github_connect_url}>
                  Install GitHub App
                </a>
              </>
            )}
          </section>
        </>
      )}
    </div>
  );
}
