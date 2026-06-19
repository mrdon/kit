import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type WidgetToken, type MintedToken } from '../api';
import { useSetChatContext } from '../chatContext';

export default function Widget() {
  useSetChatContext('the admin Chat Widget page (embeddable widget tokens)');
  const [tokens, setTokens] = useState<WidgetToken[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [origin, setOrigin] = useState('');
  const [minting, setMinting] = useState(false);
  const [minted, setMinted] = useState<MintedToken | null>(null);

  const load = () => {
    api
      .widgetTokens()
      .then((r) => setTokens(r.tokens))
      .catch((e) => setErr(e.message));
  };
  useEffect(load, []);

  const mint = async (e: React.FormEvent) => {
    e.preventDefault();
    setErr(null);
    setMinting(true);
    try {
      const m = await api.mintWidgetToken(origin.trim());
      setMinted(m);
      setOrigin('');
      load();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setMinting(false);
    }
  };

  const revoke = async (id: string) => {
    setErr(null);
    try {
      await api.revokeWidgetToken(id);
      load();
    } catch (e) {
      setErr((e as Error).message);
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
          <span>Chat widget</span>
        </nav>
        <h1>Website chat widget</h1>
        <p className="page-sub">
          Mint a token per site origin, then paste the snippet into that
          site. Revoke a token to disable the widget there.
        </p>
      </div>

      {err && <p className="banner banner-error">{err}</p>}

      <section className="panel">
        <h2 className="panel-title">Mint a token</h2>
        <form className="inline-form" onSubmit={mint}>
          <input
            type="url"
            required
            placeholder="https://example.com"
            value={origin}
            onChange={(e) => setOrigin(e.target.value)}
          />
          <button className="btn" type="submit" disabled={minting}>
            {minting ? 'Minting…' : 'Mint token'}
          </button>
        </form>
        {minted && (
          <div className="snippet-box">
            <p className="muted">
              Token for {minted.allowed_origins.join(', ')} — paste this into
              the site:
            </p>
            <pre className="snippet">{minted.embed_snippet}</pre>
          </div>
        )}
      </section>

      <section className="card-list">
        {tokens?.length === 0 && <p className="muted">No tokens yet.</p>}
        {tokens?.map((t) => (
          <article key={t.id} className="card">
            <div className="card-main">
              <span className="card-title">{t.allowed_origins.join(', ')}</span>
              <span className="card-desc">
                {t.placeholder} · created {t.created_at}
                {t.last_used_at ? ` · last used ${t.last_used_at}` : ' · never used'}
              </span>
            </div>
            <div className="card-side">
              <button className="btn btn-danger" onClick={() => revoke(t.id)}>
                Revoke
              </button>
            </div>
          </article>
        ))}
      </section>
    </div>
  );
}
