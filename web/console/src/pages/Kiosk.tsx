import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type KioskBoard } from '../api';
import { useSetChatContext } from '../chatContext';

// A board's public URL never changes, so the useful thing to show next to it
// is whether a screen is actually asking for it. Anything under ~5 minutes is
// a healthy poll; beyond that the machine is off, asleep, or off the network.
const STALE_AFTER_MS = 5 * 60 * 1000;

function seenLabel(lastSeen: string | null): { text: string; cls: string } {
  if (!lastSeen) return { text: 'Never polled', cls: 'pill pill-off' };
  const age = Date.now() - new Date(lastSeen).getTime();
  if (age < STALE_AFTER_MS) return { text: 'Live', cls: 'pill pill-ok' };
  return { text: `Last seen ${relative(age)} ago`, cls: 'pill pill-error' };
}

function relative(ms: number): string {
  const mins = Math.floor(ms / 60000);
  if (mins < 60) return `${mins}m`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h`;
  return `${Math.floor(hours / 24)}d`;
}

interface DraftState {
  name: string;
  key: string;
  url: string;
  notes: string;
}

const emptyDraft: DraftState = { name: '', key: '', url: '', notes: '' };

export default function Kiosk() {
  useSetChatContext('the Kiosk screens page');
  const [boards, setBoards] = useState<KioskBoard[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [draft, setDraft] = useState<DraftState>(emptyDraft);
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<string | null>(null);
  const [editDraft, setEditDraft] = useState<DraftState>(emptyDraft);
  const [busy, setBusy] = useState(false);
  const [copied, setCopied] = useState<string | null>(null);

  const load = () => {
    api
      .kioskBoards()
      .then((r) => setBoards(r.boards))
      .catch((e) => setErr(e.message));
  };
  useEffect(load, []);

  const create = async (e: React.FormEvent) => {
    e.preventDefault();
    setErr(null);
    setCreating(true);
    try {
      await api.createKioskBoard({
        name: draft.name.trim(),
        key: draft.key.trim(),
        url: draft.url.trim(),
        notes: draft.notes.trim(),
      });
      setDraft(emptyDraft);
      load();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setCreating(false);
    }
  };

  const startEdit = (b: KioskBoard) => {
    setEditing(b.id);
    setEditDraft({ name: b.name, key: b.key, url: b.url, notes: b.notes });
    setErr(null);
  };

  const save = async (id: string) => {
    setErr(null);
    setBusy(true);
    try {
      await api.updateKioskBoard(id, {
        name: editDraft.name.trim(),
        key: editDraft.key.trim(),
        url: editDraft.url.trim(),
        notes: editDraft.notes.trim(),
      });
      setEditing(null);
      load();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const remove = async (b: KioskBoard) => {
    setErr(null);
    setBusy(true);
    try {
      await api.deleteKioskBoard(b.id);
      load();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const copy = (b: KioskBoard) => {
    navigator.clipboard?.writeText(b.public_url).then(
      () => {
        setCopied(b.id);
        setTimeout(() => setCopied(null), 1500);
      },
      () => setErr('Could not copy to the clipboard'),
    );
  };

  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <span>Kiosk</span>
        </nav>
        <h1>Kiosk screens</h1>
        <p className="page-sub">
          Each screen gets a permanent Kit address. Point the machine's browser
          at it once; after that, changing the URL here changes what the screen
          shows.
        </p>
      </div>

      {err && <p className="banner banner-error">{err}</p>}

      <section className="panel">
        <h2 className="panel-title">Add a screen</h2>
        <form className="stack-form" onSubmit={create}>
          <div className="field-row">
            <label className="field">
              Name
              <input
                required
                placeholder="Lobby TV"
                value={draft.name}
                onChange={(e) => setDraft({ ...draft, name: e.target.value })}
              />
            </label>
            <label className="field">
              Address key
              <input
                placeholder="lobby-tv"
                value={draft.key}
                onChange={(e) => setDraft({ ...draft, key: e.target.value })}
              />
              <span className="field-note">
                Optional — derived from the name. This is what goes on the
                machine, so pick something you can retype.
              </span>
            </label>
          </div>
          <label className="field">
            Shows
            <input
              type="url"
              placeholder="https://example.com/dashboard"
              value={draft.url}
              onChange={(e) => setDraft({ ...draft, url: e.target.value })}
            />
            <span className="field-note">
              Leave blank to provision the screen now and decide later.
            </span>
          </label>
          <div>
            <button className="btn" type="submit" disabled={creating}>
              {creating ? 'Adding…' : 'Add screen'}
            </button>
          </div>
        </form>
      </section>

      <section className="card-list">
        {boards?.length === 0 && (
          <p className="muted">No screens yet.</p>
        )}
        {boards?.map((b) => {
          const seen = seenLabel(b.last_seen_at);
          if (editing === b.id) {
            return (
              <article key={b.id} className="card">
                <div className="card-main">
                  <div className="field-row">
                    <label className="field">
                      Name
                      <input
                        value={editDraft.name}
                        onChange={(e) =>
                          setEditDraft({ ...editDraft, name: e.target.value })
                        }
                      />
                    </label>
                    <label className="field">
                      Address key
                      <input
                        value={editDraft.key}
                        onChange={(e) =>
                          setEditDraft({ ...editDraft, key: e.target.value })
                        }
                      />
                      <span className="field-hint">
                        Changing this changes the screen's address — the
                        machine will keep asking for the old one until someone
                        reconfigures it.
                      </span>
                    </label>
                  </div>
                  <label className="field">
                    Shows
                    <input
                      type="url"
                      placeholder="https://example.com/dashboard"
                      value={editDraft.url}
                      onChange={(e) =>
                        setEditDraft({ ...editDraft, url: e.target.value })
                      }
                    />
                  </label>
                  {b.recent_urls && b.recent_urls.length > 0 && (
                    <div className="field">
                      Previously
                      <ul className="kiosk-history">
                        {b.recent_urls.map((h) => (
                          <li key={h.replaced_at + h.url}>
                            <code title={h.url}>{h.url}</code>
                            <span className="kiosk-history-when">
                              replaced {relative(Date.now() - new Date(h.replaced_at).getTime())} ago
                            </span>
                            <button
                              className="btn btn-sm btn-ghost"
                              type="button"
                              disabled={busy || editDraft.url === h.url}
                              onClick={() => setEditDraft({ ...editDraft, url: h.url })}
                            >
                              Use this
                            </button>
                          </li>
                        ))}
                      </ul>
                      <span className="field-note">
                        The last {b.recent_urls.length === 1 ? 'address' : `${b.recent_urls.length} addresses`} this
                        screen showed. Picking one fills the field above — you still have to Save.
                      </span>
                    </div>
                  )}
                  <label className="field">
                    Notes
                    <input
                      placeholder="Where this screen is, who to call"
                      value={editDraft.notes}
                      onChange={(e) =>
                        setEditDraft({ ...editDraft, notes: e.target.value })
                      }
                    />
                  </label>
                  <div className="drawer-actions">
                    <button
                      className="btn"
                      disabled={busy}
                      onClick={() => save(b.id)}
                    >
                      Save
                    </button>
                    <button
                      className="btn btn-ghost"
                      disabled={busy}
                      onClick={() => setEditing(null)}
                    >
                      Cancel
                    </button>
                    <button
                      className="btn btn-danger"
                      disabled={busy}
                      onClick={() => remove(b)}
                    >
                      Delete screen
                    </button>
                  </div>
                </div>
              </article>
            );
          }
          return (
            <article key={b.id} className="card">
              <div className="card-main">
                <span className="card-title">
                  {b.name} <span className={seen.cls}>{seen.text}</span>
                </span>
                <span className="card-desc">
                  {b.url ? (
                    <>
                      Shows <code>{b.url}</code>
                    </>
                  ) : (
                    <em>Nothing assigned yet</em>
                  )}
                </span>
                <span className="card-desc">
                  Screen address: <code>{b.public_url}</code>
                </span>
                {b.notes && <span className="card-desc">{b.notes}</span>}
              </div>
              <div className="card-side">
                <button className="btn btn-sm" onClick={() => copy(b)}>
                  {copied === b.id ? 'Copied' : 'Copy address'}
                </button>
                <button className="btn btn-sm btn-ghost" onClick={() => startEdit(b)}>
                  Edit
                </button>
              </div>
            </article>
          );
        })}
      </section>

      <section className="panel">
        <h2 className="panel-title">Setting up a machine</h2>
        <p className="muted">
          Open the screen's address in the kiosk browser — it redirects to
          whatever the screen currently shows. To make the screen follow later
          changes on its own, run this alongside the browser. It asks Kit where
          the screen should point, and reloads the browser only when the answer
          changes.
        </p>
        <pre className="snippet">{POLLER_SNIPPET}</pre>
        <p className="muted">
          The address is public and unauthenticated by design — anyone who
          knows it can see where the screen points, so don't send a screen to a
          URL that is itself a private link.
        </p>
      </section>
    </div>
  );
}

// A deliberately dependency-free poller: curl, a saved copy of the last
// target, and xdotool to drive whatever browser is already on screen. It reads
// the Location header WITHOUT following it — following the redirect would take
// the poller to the content and leave nothing behind to keep asking.
const POLLER_SNIPPET = `#!/bin/sh
# Repoint this kiosk whenever the board's target changes.
BOARD_URL="https://kit.example.com/acme/kiosk/lobby-tv"
STATE="$HOME/.kiosk-current"

while true; do
  target=$(curl -s -o /dev/null -w '%{redirect_url}' "$BOARD_URL")
  if [ -n "$target" ] && [ "$target" != "$(cat "$STATE" 2>/dev/null)" ]; then
    printf '%s' "$target" > "$STATE"
    xdotool key --clearmodifiers ctrl+l
    xdotool type --clearmodifiers "$target"
    xdotool key --clearmodifiers Return
  fi
  sleep 30
done`;
