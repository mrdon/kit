import { useState } from 'react';
import { Link } from 'react-router-dom';
import { useSetChatContext } from '../../chatContext';
import { SLUG } from '../../workspace';
import { PdfPane } from './PdfPane';

// The table topper, from the bar iPad: this week's card or next week's.
// Next week is the Friday case, which is why it gets a button of its own
// rather than a date to work out.

export default function DeviceTopper() {
  useSetChatContext('the table topper page on a taproom device');
  const [week, setWeek] = useState<'this' | 'next' | null>(null);
  const href = (w: string) => `/${SLUG}/events/topper.pdf?week=${w}`;
  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <span>Table topper</span>
        </nav>
        <h1>Table topper</h1>
        <p className="page-sub">The week’s events on one sheet for the tables. Pick a week, then press Print.</p>
      </div>
      <section className="device-actions device-actions-row">
        <button className={`btn device-btn${week === 'this' ? ' selected' : ''}`} type="button" onClick={() => setWeek('this')}>
          This week
        </button>
        <button className={`btn device-btn${week === 'next' ? ' selected' : ''}`} type="button" onClick={() => setWeek('next')}>
          Next week
        </button>
      </section>
      {week && <PdfPane src={href(week)} title={week === 'this' ? 'This week’s topper' : 'Next week’s topper'} />}
    </div>
  );
}
