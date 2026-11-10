import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api } from '../../api';
import { useSetChatContext } from '../../chatContext';
import { SLUG } from '../../workspace';
import { PdfPane } from './PdfPane';

// Print the menu, from the bar iPad. The menu follows Untappd on its own, so
// there is nothing to refresh first: open it and print. What it does warn
// about is a beer with no description, because Kit cannot fetch them and the
// fix is a minute on the descriptions page before the paper goes out.

export default function DevicePrint() {
  useSetChatContext('the print-the-menu page on a taproom device');
  const [missing, setMissing] = useState<string[]>([]);
  // The PDF is fetched when asked for; the query string changes so the
  // viewer never shows a cached menu.
  const [shownAt, setShownAt] = useState<number | null>(null);

  useEffect(() => {
    api
      .menuPrintNotes()
      .then((d) => setMissing(d.missing ?? []))
      .catch(() => setMissing([]));
  }, [shownAt]);

  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <span>Print the menu</span>
        </nav>
        <h1>Print the menu</h1>
        <p className="page-sub">Open the menu and press Print. It already has today&rsquo;s taps.</p>
      </div>
      {missing.length > 0 && (
        <p className="banner banner-error">
          No description yet, so these print without one: {missing.join(', ')}.{' '}
          <Link to="/device/descriptions">Write them</Link> first if you can.
        </p>
      )}
      <section className="device-actions">
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
