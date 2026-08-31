import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type MenuBoard } from '../api';
import { useSetChatContext } from '../chatContext';

// The Menu page exists to answer one question: what URL do I paste into the
// screen? The tap list follows Untappd and the presentation is pushed from an
// AI client, so there is nothing to edit here — an address, a copy button, and
// enough state to tell whether it is working.
export default function Menu() {
  useSetChatContext('the Menu board page');
  const [board, setBoard] = useState<MenuBoard | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const field = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    api
      .menuBoard()
      .then(setBoard)
      .catch((e) => setErr(e instanceof Error ? e.message : String(e)));
  }, []);

  // The address is shown in a real text field and copying falls back to
  // selecting it. navigator.clipboard is not always available — an extension
  // can block it, and it needs a secure context — and when it fails the
  // address must still be gettable rather than the button just not working.
  function copy() {
    if (!board) return;
    field.current?.select();
    navigator.clipboard?.writeText(board.public_url).then(
      () => {
        setCopied(true);
        setTimeout(() => setCopied(false), 2000);
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
        <h1>Menu board</h1>
        <p className="page-sub">
          Your menu has a permanent address. Paste it into a kiosk screen once;
          after that the tap list keeps itself current.
        </p>
      </div>

      {err && <p className="banner banner-error">{err}</p>}
      {!board && !err && <p className="muted">Loading…</p>}

      {board && (
        <section className="panel">
          <label className="field menu-address">
            Screen address
            <input
              ref={field}
              readOnly
              value={board.public_url}
              onFocus={(e) => e.currentTarget.select()}
              spellCheck={false}
            />
          </label>

          <p className="card-desc">
            {board.parse_error ? (
              <em className="error-text">Will not render: {board.parse_error}</em>
            ) : board.empty ? (
              <em>
                No tap list yet — the screen shows a placeholder until there is
                one.
              </em>
            ) : (
              <>
                {board.taps} taps, {board.panels} panels
                {board.updated_at && (
                  <> · last changed {new Date(board.updated_at).toLocaleString()}</>
                )}
              </>
            )}
          </p>

          <p className="card-desc">
            {board.source ? (
              <>
                Following Untappd board <code>{board.source}</code>
                {board.synced_at && (
                  <> · last checked {new Date(board.synced_at).toLocaleTimeString()}</>
                )}
              </>
            ) : (
              <em>Set by hand — not following anything.</em>
            )}
          </p>

          {board.sync_error && (
            <p className="banner banner-error">
              The last check failed: {board.sync_error}. The screen is still
              showing the tap list it had.
            </p>
          )}

          <div className="drawer-actions">
            <button className="btn" onClick={copy}>
              {copied ? 'Copied' : 'Copy address'}
            </button>
            <a
              className="btn btn-ghost"
              href={board.public_url}
              target="_blank"
              rel="noreferrer"
            >
              Preview
            </a>
          </div>
        </section>
      )}
    </div>
  );
}
