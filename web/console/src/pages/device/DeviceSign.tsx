import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type SignChoice } from '../../api';
import { useSetChatContext } from '../../chatContext';
import { SLUG } from '../../workspace';
import { PdfPane } from './PdfPane';

// The entrance sign, from the bar iPad: today's events on one letter page
// for the stand by the door. Some nights the event's own poster is the
// better sign, so each event that has one is offered as well.

type Day = 'today' | 'tomorrow';

export default function DeviceSign() {
  useSetChatContext('the entrance sign page on a taproom device');
  const [day, setDay] = useState<Day>('today');
  const [date, setDate] = useState('');
  const [events, setEvents] = useState<SignChoice[]>([]);
  const [err, setErr] = useState<string | null>(null);
  // '' is the generated sign; an event id is that event's poster.
  const [pick, setPick] = useState<string | null>(null);

  useEffect(() => {
    setPick(null);
    setErr(null);
    api
      .eventsSign(day)
      .then((r) => {
        setDate(r.date);
        setEvents(r.events ?? []);
      })
      .catch((e) => setErr(e instanceof Error ? e.message : String(e)));
  }, [day]);

  const posters = events.filter((e) => e.has_poster);
  const src =
    pick === null ? null : `/${SLUG}/events/sign.pdf?day=${day}` + (pick ? `&poster=${encodeURIComponent(pick)}` : '');
  const chosen = posters.find((e) => e.id === pick);

  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <span>Entrance sign</span>
        </nav>
        <h1>Entrance sign</h1>
        <p className="page-sub">
          {date ? `${date}. ` : ''}Print the sign with everything on, or one event’s own poster, then press Print.
        </p>
      </div>
      {err && <p className="banner banner-error">{err}</p>}
      <div className="device-group">
        <p className="device-group-label">Day</p>
        <section className="device-actions device-actions-row">
          {(['today', 'tomorrow'] as Day[]).map((d) => (
            <button
              key={d}
              className={`btn device-btn${day === d ? ' selected' : ''}`}
              type="button"
              onClick={() => setDay(d)}
            >
              {d === 'today' ? 'Today' : 'Tomorrow'}
            </button>
          ))}
        </section>
      </div>
      <div className="device-group">
        <p className="device-group-label">Print</p>
        <section className="device-actions">
          <button
            className={`btn device-btn${pick === '' ? ' selected' : ''}`}
            type="button"
            onClick={() => setPick('')}
          >
            {events.length ? 'Sign with everything on' : 'Sign (nothing on the calendar)'}
          </button>
          {posters.map((e) => (
            <button
              key={e.id}
              className={`btn device-btn${pick === e.id ? ' selected' : ''}`}
              type="button"
              onClick={() => setPick(e.id)}
            >
              {e.title} poster
            </button>
          ))}
        </section>
      </div>
      {src && <PdfPane src={src} title={chosen ? `${chosen.title} poster` : 'Entrance sign'} />}
    </div>
  );
}
