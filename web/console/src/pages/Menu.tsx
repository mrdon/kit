import { useEffect, useRef, useState } from 'react';
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
  const fields = useRef<Record<string, HTMLInputElement | null>>({});

  useEffect(() => {
    api
      .menuBoards()
      .then((r) => setBoards(r.boards))
      .catch((e) => setErr(e instanceof Error ? e.message : String(e)));
  }, []);

  // The address is shown in a real text field, and copying falls back to
  // selecting it. navigator.clipboard is not always available — an extension
  // can block it, and it needs a secure context — and when it fails the
  // address must still be gettable rather than the button just not working.
  function copy(b: MenuBoard) {
    const field = fields.current[b.key];
    field?.select();
    navigator.clipboard?.writeText(b.public_url).then(
      () => {
        setCopied(b.key);
        setTimeout(() => setCopied(null), 2000);
      },
      () => setErr('Copying was blocked. The address is selected — press ⌘C.'),
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
          Your menu has a permanent address. Paste it into a kiosk screen once;
          after that, changing the tap list changes what the screen shows.
        </p>
      </div>

      {err && <p className="banner banner-error">{err}</p>}

      {boards === null && !err && <p className="muted">Loading…</p>}

      <section className="card-list">
        {boards?.map((b) => (
          <article key={b.key} className="card">
            <div className="card-main">
              <span className="card-title">{b.name}</span>
              <label className="card-desc menu-address">
                Screen address
                <input
                  ref={(el) => {
                    fields.current[b.key] = el;
                  }}
                  readOnly
                  value={b.public_url}
                  onFocus={(e) => e.currentTarget.select()}
                  spellCheck={false}
                />
              </label>
              <span className="card-desc">
                {b.error ? (
                  <em className="error-text">Will not render: {b.error}</em>
                ) : b.empty ? (
                  <em>
                    No tap list set yet — the screen shows a placeholder until
                    there is one.
                  </em>
                ) : (
                  <>
                    {b.taps} taps, {b.panels} panels · updated{' '}
                    {b.updated_at && new Date(b.updated_at).toLocaleString()}
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
