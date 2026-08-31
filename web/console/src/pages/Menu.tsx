import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type MenuBoard } from '../api';
import { useSetChatContext } from '../chatContext';

// The Menu page exists to answer one question: what URL do I paste into the
// screen? Boards are authored and published from elsewhere, so there is
// nothing to edit here — a list of addresses with a copy button is the whole
// job, and adding a form before there is an editing surface would just be
// half a feature.
export default function Menu() {
  useSetChatContext('the Menu boards page');
  const [boards, setBoards] = useState<MenuBoard[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [copied, setCopied] = useState<string | null>(null);

  useEffect(() => {
    api
      .menuBoards()
      .then((r) => setBoards(r.boards))
      .catch((e) => setErr(e instanceof Error ? e.message : String(e)));
  }, []);

  function copy(b: MenuBoard) {
    navigator.clipboard?.writeText(b.public_url).then(
      () => {
        setCopied(b.key);
        setTimeout(() => setCopied(null), 2000);
      },
      () => setErr('Could not copy — select the address and copy it by hand.'),
    );
  }

  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <span>Menu</span>
        </nav>
        <h1>Menu boards</h1>
        <p className="page-sub">
          Published tap lists. Copy a board's address and set it as a kiosk
          screen's URL to put it on the wall.
        </p>
      </div>

      {err && <p className="banner banner-error">{err}</p>}

      {boards === null && !err && <p className="muted">Loading…</p>}

      {boards?.length === 0 && (
        <p className="muted">
          No boards published yet. Publish one with the <code>set_menu_board</code>{' '}
          tool from an AI client, then its address will appear here.
        </p>
      )}

      <section className="card-list">
        {boards?.map((b) => (
          <article key={b.key} className="card">
            <div className="card-main">
              <span className="card-title">{b.name}</span>
              <span className="card-desc">
                Screen address: <code>{b.public_url}</code>
              </span>
              <span className="card-desc">
                {b.error ? (
                  <em className="error-text">Will not render: {b.error}</em>
                ) : (
                  <>
                    {b.taps} taps, {b.panels} panels · published{' '}
                    {new Date(b.updated_at).toLocaleString()}
                  </>
                )}
              </span>
            </div>
            <div className="card-side">
              <button className="btn btn-sm" onClick={() => copy(b)}>
                {copied === b.key ? 'Copied' : 'Copy address'}
              </button>
              <a
                className="btn btn-sm btn-ghost"
                href={b.public_url}
                target="_blank"
                rel="noreferrer"
              >
                Preview
              </a>
            </div>
          </article>
        ))}
      </section>
    </div>
  );
}
