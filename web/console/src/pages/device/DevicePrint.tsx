import { useState } from 'react';
import { Link } from 'react-router-dom';
import { api } from '../../api';
import { useSetChatContext } from '../../chatContext';
import { SLUG } from '../../workspace';
import { PdfPane } from './PdfPane';

// Print the menu, from the bar iPad. Two steps on purpose: the paper menu
// prints from a stored tap list that only changes when somebody syncs it
// (there is no cron, by design), so after a keg change "Print" alone would
// hand out last week's beers. Sync first, read the summary, then open.

export default function DevicePrint() {
  useSetChatContext('the print-the-menu page on a taproom device');
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<{ text: string; ok: boolean } | null>(null);
  // The PDF is fetched when asked for, and again after every sync: the
  // query string changes so the viewer never shows a cached menu.
  const [shownAt, setShownAt] = useState<number | null>(null);

  async function sync() {
    setBusy(true);
    setNote(null);
    try {
      const res = await api.syncMenuPrint();
      setNote({ text: res.summary || 'Tap list refreshed.', ok: !res.state.sync_error });
      if (shownAt !== null) setShownAt(Date.now());
    } catch (e) {
      setNote({ text: e instanceof Error ? e.message : String(e), ok: false });
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <span>Print the menu</span>
        </nav>
        <h1>Print the menu</h1>
        <p className="page-sub">
          Refresh the tap list first if a keg changed, write a description for anything new, then open the menu and press Print.
        </p>
      </div>
      {note && <p className={note.ok ? 'banner banner-ok' : 'banner banner-error'}>{note.text}</p>}
      <section className="device-actions">
        <button className="btn device-btn" type="button" onClick={() => void sync()} disabled={busy}>
          {busy ? 'Refreshing…' : 'Refresh tap list'}
        </button>
        <button className="btn device-btn" type="button" onClick={() => setShownAt(Date.now())}>
          {shownAt === null ? 'Open the menu' : 'Reload the menu'}
        </button>
        <Link className="btn device-btn-sm" to="/device/descriptions">
          Beer descriptions
        </Link>
      </section>
      {shownAt !== null && (
        <PdfPane src={`/${SLUG}/menu/print.pdf?t=${shownAt}`} title="Printed menu" />
      )}
    </div>
  );
}
