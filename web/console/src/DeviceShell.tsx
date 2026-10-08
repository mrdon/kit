import { Link } from 'react-router-dom';
import type { Me } from './api';

// The chrome and the home screen a paired device sees. One shell serves
// every device kind: the trivia laptop is this shell showing a single tile,
// the bar iPad shows four or five. Which tiles appear is decided by the
// capabilities the admin ticked at pairing; the server enforces the same
// list on every API behind them.

interface DeviceTile {
  cap: string;
  to: string;
  title: string;
  blurb: string;
}

// One tile per capability that has a screen. Capabilities without a tile
// yet (the taproom ones land in the next phase) simply don't show.
const DEVICE_TILES: DeviceTile[] = [
  {
    cap: 'trivia.host',
    to: '/trivia',
    title: 'Host trivia',
    blurb: 'Run tonight’s quiz: the board, the timer, the scores.',
  },
];

export function deviceTiles(me: Me): DeviceTile[] {
  const caps = new Set(me.capabilities ?? []);
  return DEVICE_TILES.filter((t) => caps.has(t.cap));
}

export function DeviceBar({ me }: { me: Me }) {
  return (
    <header className="devicebar">
      <Link to="/" className="devicebar-brand">
        {me.workspace_icon_url && (
          <img src={me.workspace_icon_url} alt="" width={28} height={28} className="topbar-icon" />
        )}
        <span>{me.workspace_name}</span>
      </Link>
      <span className="devicebar-label">{me.label}</span>
    </header>
  );
}

export function DeviceHome({ me }: { me: Me }) {
  const tiles = deviceTiles(me);
  return (
    <div className="page">
      <div className="page-head">
        <h1>{me.label}</h1>
      </div>
      {tiles.length === 0 ? (
        <p className="device-empty">
          This device has nothing to do yet. An admin can give it something on the Devices page.
        </p>
      ) : (
        <section className="device-tile-grid">
          {tiles.map((t) => (
            <Link key={t.cap} to={t.to} className="tile device-tile">
              <span className="tile-title">{t.title}</span>
              <span className="tile-blurb">{t.blurb}</span>
            </Link>
          ))}
        </section>
      )}
    </div>
  );
}
