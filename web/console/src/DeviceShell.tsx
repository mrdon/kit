import { Link, useLocation } from 'react-router-dom';
import type { Me } from './api';

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
    to: '/device/print',
    title: 'Print the menu',
    blurb: 'Refresh the tap list, then open the paper menu to print.',
  },
  {
    cap: 'events.topper',
    to: '/device/topper',
    title: 'Table topper',
    blurb: 'Print this week’s or next week’s events card.',
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

// The bar's one control is a Home button, shown everywhere but home. A
// brand link alone reads as decoration on a tablet; a labelled button with
// a chevron reads as "back to the tiles", which is the only navigation a
// device has.
export function DeviceBar({ me }: { me: Me }) {
  const atHome = useLocation().pathname === '/';
  return (
    <header className="devicebar">
      <div className="devicebar-left">
        {!atHome && (
          <Link to="/" className="devicebar-home">
            <span aria-hidden="true">‹</span> Home
          </Link>
        )}
        <span className="devicebar-brand">
          {me.workspace_icon_url && (
            <img src={me.workspace_icon_url} alt="" width={28} height={28} className="topbar-icon" />
          )}
          <span>{me.workspace_name}</span>
        </span>
      </div>
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
