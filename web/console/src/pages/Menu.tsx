import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type MenuBoard } from '../api';
import { useSetChatContext } from '../chatContext';
import { SLUG } from '../workspace';

// The Menu page exists to answer one question: what URL do I paste into the
// screen? The tap list follows Untappd and the presentation is pushed from an
// AI client, so there is nothing to edit here — an address, a copy button, and
// enough state to tell whether it is working.
//
// The printed menu hangs off this page because it is the same tap list on
// paper. The events table topper is a separate thing on a separate page: that
// one is about the week's programme, this one is about what is pouring.
//
// It gets its own titled panel rather than a slot under a cog. The Events page
// hides its topper there because that page head already carries three buttons,
// so a cog reads as "the rest of them"; this page head is a title and nothing
// else, where a lone cog is just an unlabelled icon nobody thinks to press.

// openPrintMenu opens the letter-sized tap list in its own tab, the same way
// the events topper does. The next thing anyone does with it is press print,
// so it wants a viewer rather than a download.
function openPrintMenu() {
  window.open(`/${SLUG}/menu/print.pdf`, '_blank', 'noopener');
}

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

      {board && !board.empty && (
        <section className="panel">
          <h2 className="panel-title">Printed menu</h2>
          <p className="card-desc">
            The same tap list on paper, sized for letter and ready to print —
            a page per few sections, with each beer&rsquo;s style, strength,
            price and description. It is built when you open it, so it always
            shows what is pouring right now.
          </p>
          <p className="card-desc">
            Only beers Untappd prices for a 4oz taster appear, which is every
            beer actually on tap. Cans, sodas and the wording around the edges
            are set with <code>set_menu_print</code>.
          </p>
          <div className="drawer-actions">
            <button className="btn" onClick={openPrintMenu}>
              Open printable menu
            </button>
            <Link className="btn btn-ghost" to="/admin/menu">
              Settings
            </Link>
          </div>
        </section>
      )}
    </div>
  );
}
