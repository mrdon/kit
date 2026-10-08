import { Link } from 'react-router-dom';
import type { Me } from './api';
import { SLUG } from './workspace';

// The chrome and the home screen a paired device sees. One shell serves
// every device kind: the trivia laptop is this shell showing a single tile,
// the bar iPad shows four or five. Which tiles appear is decided by the
// capabilities the admin ticked at pairing; the server enforces the same
// list on every API behind them.

interface DeviceTile {
  cap: string;
  // A client route, or an absolute href that opens in a new tab (the
  // printed menu is a PDF: Safari's share sheet is how it reaches AirPrint).
  to?: string;
  href?: string;
  title: string;
  blurb: string;
}

// One tile per capability. A device holding every taproom capability plus
// trivia shows all five; the trivia laptop shows one.
const DEVICE_TILES: DeviceTile[] = [
  {
    cap: 'menu.happy_hour',
    to: '/device/happy-hour',
    title: 'Happy hour',
    blurb: 'Start it or end it. The board and Square follow.',
  },
  {
    cap: 'menu.print',
    href: `/${SLUG}/menu/print.pdf`,
    title: 'Print the menu',
    blurb: 'Opens the paper menu; print it from the share button.',
  },
  {
    cap: 'menu.gluten_reduced',
    to: '/device/gluten-reduced',
    title: 'Gluten reduced',
    blurb: 'Tick the beers that are. The screen and the menu mark them.',
  },
  {
    cap: 'kiosk.repoint',
    to: '/device/screens',
    title: 'Wall screens',
    blurb: 'Change what a screen is showing.',
  },
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
          {tiles.map((t) =>
            t.href ? (
              <a key={t.cap} href={t.href} target="_blank" rel="noopener" className="tile device-tile">
                <span className="tile-title">{t.title}</span>
                <span className="tile-blurb">{t.blurb}</span>
              </a>
            ) : (
              <Link key={t.cap} to={t.to ?? '/'} className="tile device-tile">
                <span className="tile-title">{t.title}</span>
                <span className="tile-blurb">{t.blurb}</span>
              </Link>
            ),
          )}
        </section>
      )}
    </div>
  );
}
