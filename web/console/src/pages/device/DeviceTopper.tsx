import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type LibraryTopper } from '../../api';
import { useSetChatContext } from '../../chatContext';
import { SLUG } from '../../workspace';
import { PdfPane } from './PdfPane';

// The table topper, from the bar iPad: this week's card or next week's.
// Next week is the Friday case, which is why it gets a button of its own
// rather than a date to work out. Below those, the ready-made toppers in the
// library (happy hour, a beer feature, a weekend sale), printed as uploaded.

type Pick = { kind: 'week'; week: 'this' | 'next' } | { kind: 'library'; topper: LibraryTopper };

function pickSrc(pick: Pick): string {
  if (pick.kind === 'week') return `/${SLUG}/events/topper.pdf?week=${pick.week}`;
  // updated_at in the URL so a replaced file is never served from a stale viewer cache.
  return `/${SLUG}/events/topper-library/${pick.topper.id}/pdf?v=${encodeURIComponent(pick.topper.updated_at)}`;
}

function pickTitle(pick: Pick): string {
  if (pick.kind === 'library') return pick.topper.title;
  return pick.week === 'this' ? 'This week’s topper' : 'Next week’s topper';
}

export default function DeviceTopper() {
  useSetChatContext('the table topper page on a taproom device');
  const [pick, setPick] = useState<Pick | null>(null);
  const [library, setLibrary] = useState<LibraryTopper[]>([]);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    api
      .eventsTopperLibrary()
      .then((r) => setLibrary(r.toppers ?? []))
      .catch((e) => setErr(e instanceof Error ? e.message : String(e)));
  }, []);

  const weekSelected = (w: 'this' | 'next') => pick?.kind === 'week' && pick.week === w;
  const topperSelected = (t: LibraryTopper) => pick?.kind === 'library' && pick.topper.id === t.id;

  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <span>Table topper</span>
        </nav>
        <h1>Table topper</h1>
        <p className="page-sub">Pick a topper, then press Print.</p>
      </div>
      {err && <p className="banner banner-error">{err}</p>}
      <div className="device-group">
        <p className="device-group-label">The week’s events</p>
        <section className="device-actions device-actions-row">
          {(['this', 'next'] as const).map((w) => (
            <button
              key={w}
              className={`btn device-btn${weekSelected(w) ? ' selected' : ''}`}
              type="button"
              onClick={() => setPick({ kind: 'week', week: w })}
            >
              {w === 'this' ? 'This week' : 'Next week'}
            </button>
          ))}
        </section>
      </div>
      {library.length > 0 && (
        <div className="device-group">
          <p className="device-group-label">Ready to print</p>
          <section className="device-actions">
            {library.map((t) => (
              <button
                key={t.id}
                className={`btn device-btn${topperSelected(t) ? ' selected' : ''}`}
                type="button"
                onClick={() => setPick({ kind: 'library', topper: t })}
              >
                {t.title}
              </button>
            ))}
          </section>
        </div>
      )}
      {pick && <PdfPane src={pickSrc(pick)} title={pickTitle(pick)} />}
    </div>
  );
}
