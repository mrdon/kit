import { useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type KioskBoard, type KioskDestination } from '../../api';
import { useSetChatContext } from '../../chatContext';
import ScreenPicker from '../../ScreenPicker';
import { usePolled } from '../../usePolled';

// Repoint a wall screen from the bar iPad. One row per board: its name and a
// button per Kit screen page. No creating or deleting boards here; that is
// setup, done once from the console.

export default function DeviceScreens() {
  useSetChatContext('the wall screens page on a taproom device');
  const [boards, setBoards] = useState<KioskBoard[] | null>(null);
  const [destinations, setDestinations] = useState<KioskDestination[]>([]);
  const [err, setErr] = useState<string | null>(null);

  // Polled: a board repointed from the console lights up its new button here
  // within the interval.
  const load = () =>
    api
      .kioskBoards()
      .then((r) => {
        setBoards(r.boards);
        setDestinations(r.destinations);
      })
      .catch((e) => setErr(e instanceof Error ? e.message : String(e)));
  usePolled(load);

  async function repoint(b: KioskBoard, url: string): Promise<boolean> {
    setErr(null);
    try {
      await api.updateKioskBoard(b.id, { name: b.name, key: b.key, url, notes: b.notes });
      await load();
      return true;
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
      return false;
    }
  }

  return (
    <div className="page device-wide">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <span>Wall screens</span>
        </nav>
        <h1>Wall screens</h1>
        <p className="page-sub">Tap what a screen should show. It follows within a minute.</p>
      </div>
      {err && <p className="banner banner-error">{err}</p>}
      {boards && boards.length === 0 && <p className="device-empty">No screens are set up yet.</p>}
      <ul className="card-list">
        {boards?.map((b) => (
          <li key={b.id} className="card">
            <div className="card-main">
              <div className="card-title">{b.name}</div>
              <ScreenPicker
                large
                destinations={destinations}
                current={b.url}
                recent={b.recent_urls?.map((u) => u.url)}
                onPick={(url) => repoint(b, url)}
              />
            </div>
          </li>
        ))}
      </ul>
    </div>
  );
}
