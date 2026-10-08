import { useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type KioskBoard } from '../../api';
import { useSetChatContext } from '../../chatContext';
import { usePolled } from '../../usePolled';

// Repoint a wall screen from the bar iPad. One row per board: the name, the
// address it shows now, and a box to type a new one. No creating or
// deleting boards here; that is setup, done once from the console.

export default function DeviceScreens() {
  useSetChatContext('the wall screens page on a taproom device');
  const [boards, setBoards] = useState<KioskBoard[] | null>(null);
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [savedID, setSavedID] = useState<string | null>(null);

  // Polled, merging: a board repointed from the console shows its new
  // address here within the interval, but a box somebody is typing into
  // keeps what they typed.
  const load = () =>
    api
      .kioskBoards()
      .then((r) => {
        setBoards((prev) => {
          setDrafts((d) => {
            const out: Record<string, string> = {};
            for (const b of r.boards) {
              const was = prev?.find((p) => p.id === b.id);
              const touched = d[b.id] !== undefined && d[b.id] !== (was?.url ?? '');
              out[b.id] = touched ? d[b.id] : b.url;
            }
            return out;
          });
          return r.boards;
        });
      })
      .catch((e) => setErr(e instanceof Error ? e.message : String(e)));
  usePolled(load);

  async function repoint(b: KioskBoard) {
    setBusy(b.id);
    setErr(null);
    setSavedID(null);
    try {
      await api.updateKioskBoard(b.id, {
        name: b.name,
        key: b.key,
        url: (drafts[b.id] ?? '').trim(),
        notes: b.notes,
      });
      setSavedID(b.id);
      await load();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
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
        <p className="page-sub">Type a new address and press Show. The screen follows within a minute.</p>
      </div>
      {err && <p className="banner banner-error">{err}</p>}
      {boards && boards.length === 0 && <p className="device-empty">No screens are set up yet.</p>}
      <ul className="card-list">
        {boards?.map((b) => (
          <li key={b.id} className="card">
            <div className="card-main">
              <div className="card-title">{b.name}</div>
              <form
                className="device-repoint"
                onSubmit={(e) => {
                  e.preventDefault();
                  void repoint(b);
                }}
              >
                <input
                  type="url"
                  required
                  value={drafts[b.id] ?? ''}
                  onChange={(e) => setDrafts({ ...drafts, [b.id]: e.target.value })}
                  placeholder="https://"
                />
                <button className="btn device-btn-sm" type="submit" disabled={busy === b.id || (drafts[b.id] ?? '') === b.url}>
                  {busy === b.id ? 'Saving…' : savedID === b.id ? 'Shown' : 'Show'}
                </button>
              </form>
              {b.recent_urls && b.recent_urls.length > 0 && (
                <div className="chip-row">
                  {b.recent_urls.slice(0, 4).map((u) => (
                    <button
                      key={u.url + u.replaced_at}
                      type="button"
                      className="chip chip-btn"
                      onClick={() => setDrafts({ ...drafts, [b.id]: u.url })}
                      title={u.url}
                    >
                      {u.url.replace(/^https?:\/\//, '').slice(0, 40)}
                    </button>
                  ))}
                </div>
              )}
            </div>
          </li>
        ))}
      </ul>
    </div>
  );
}
