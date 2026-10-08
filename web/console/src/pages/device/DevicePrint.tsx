import { useState } from 'react';
import { Link } from 'react-router-dom';
import { api } from '../../api';
import { useSetChatContext } from '../../chatContext';
import { SLUG } from '../../workspace';

// Print the menu, from the bar iPad. Two steps on purpose: the paper menu
// prints from a stored tap list that only changes when somebody syncs it
// (there is no cron, by design), so after a keg change "Print" alone would
// hand out last week's beers. Sync first, read the summary, then open.

export default function DevicePrint() {
  useSetChatContext('the print-the-menu page on a taproom device');
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<{ text: string; ok: boolean } | null>(null);

  async function sync() {
    setBusy(true);
    setNote(null);
    try {
      const res = await api.syncMenuPrint();
      setNote({ text: res.summary || 'Tap list refreshed.', ok: !res.state.sync_error });
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
          Refresh the tap list first if a keg changed, then open the menu and print it from the share button.
        </p>
      </div>
      {note && <p className={note.ok ? 'banner banner-ok' : 'banner banner-error'}>{note.text}</p>}
      <section className="device-actions">
        <button className="btn device-btn" type="button" onClick={() => void sync()} disabled={busy}>
          {busy ? 'Refreshing…' : 'Refresh tap list'}
        </button>
        <a className="btn device-btn" href={`/${SLUG}/menu/print.pdf`} target="_blank" rel="noopener">
          Open the menu to print
        </a>
      </section>
    </div>
  );
}
