import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type MenuBoard } from '../api';
import { useSetChatContext } from '../chatContext';
import { useMe } from '../me';
import MenuGlutenReduced from './MenuGlutenReduced';
import MenuItems from './MenuItems';
import { SLUG } from '../workspace';

// The Menu page answers what URL to paste into the screen, and holds the one
// menu both the screen and the paper read: Untappd's tap list plus Kit's
// items, hides and 86s (see MenuItems).
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
  const me = useMe();
  const [board, setBoard] = useState<MenuBoard | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [missing, setMissing] = useState<string[]>([]);
  const field = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    api
      .menuBoard()
      .then(setBoard)
      .catch((e) => setErr(e instanceof Error ? e.message : String(e)));
  }, []);

  // Descriptions are admin data, so only an admin is warned, and only an
  // admin can fix it. A failure just leaves the warning off.
  useEffect(() => {
    if (!me?.is_admin) return;
    api
      .menuPrintNotes()
      .then((d) => setMissing(d.missing ?? []))
      .catch(() => setMissing([]));
  }, [me?.is_admin]);

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

      {/* Here rather than under the printed menu's settings because it marks
          both: the screen and the paper read the same list. Admin-only, like
          the endpoint behind it. */}
      {board && !board.empty && me?.is_admin && <MenuGlutenReduced />}

      {board && !board.empty && <MenuItems isAdmin={!!me?.is_admin} />}

      {board && !board.empty && (
        <section className="panel">
          <h2 className="panel-title">Printed menu</h2>
          <p className="card-desc">
            The same tap list on paper, sized for letter and ready to print —
            a page per few sections, with each beer&rsquo;s style, strength,
            price and description, and no section ever split across a page.
          </p>
          <p className="card-desc">
            It prints the same menu as the wall, with the items, hides and 86s
            above. Descriptions are written on the settings page.
          </p>
          {missing.length > 0 && (
            <p className="banner banner-error">
              No description yet, so these print without one: {missing.join(', ')}.
            </p>
          )}
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
