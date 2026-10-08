import { Link } from 'react-router-dom';
import { useSetChatContext } from '../../chatContext';
import { SLUG } from '../../workspace';

// The table topper, from the bar iPad: this week's card or next week's.
// Next week is the Friday case, which is why it gets a button of its own
// rather than a date to work out.

export default function DeviceTopper() {
  useSetChatContext('the table topper page on a taproom device');
  const href = (week: string) => `/${SLUG}/events/topper.pdf?week=${week}`;
  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <span>Table topper</span>
        </nav>
        <h1>Table topper</h1>
        <p className="page-sub">The week’s events on one sheet for the tables. Opens as a PDF; print it from the share button.</p>
      </div>
      <section className="device-actions">
        <a className="btn device-btn" href={href('this')} target="_blank" rel="noopener">
          This week
        </a>
        <a className="btn device-btn" href={href('next')} target="_blank" rel="noopener">
          Next week
        </a>
      </section>
    </div>
  );
}
