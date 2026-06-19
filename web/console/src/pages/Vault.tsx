import { useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { useMe } from '../me';
import { vaultApi, type VaultEntrySummary, type VaultStatus } from './vault/api';
import { diceware } from './vault/crypto';
import { connectWorker, hasKey, rotate, setupVault, unlock } from './vault/worker';
import { AddPanel, RevealPanel } from './vault/panels';
import { useDetailRoute } from '../useDetailRoute';
import { useSetChatContext } from '../chatContext';

type Gate = { kind: 'add' } | { kind: 'reveal'; id: string };

export default function Vault() {
  const me = useMe();
  const detail = useDetailRoute('/vault');
  const [status, setStatus] = useState<VaultStatus | null>(null);
  const [entries, setEntries] = useState<VaultEntrySummary[] | null>(null);
  const [unlocked, setUnlocked] = useState(false);
  const [filter, setFilter] = useState('');
  const [err, setErr] = useState<string | null>(null);
  const [panel, setPanel] = useState<Gate | null>(null);
  const [pending, setPending] = useState<Gate | null>(null);
  const [showSetup, setShowSetup] = useState(false);
  const [showRotate, setShowRotate] = useState(false);

  useEffect(() => {
    connectWorker();
    hasKey().then(setUnlocked).catch(() => {});
    vaultApi<VaultStatus>('GET', '/status')
      .then(setStatus)
      .catch((e) => setErr(e.message));
  }, []);

  const loadEntries = () => {
    vaultApi<VaultEntrySummary[]>('GET', '/entries?limit=500')
      .then((rows) => setEntries(Array.isArray(rows) ? rows : []))
      .catch((e) => setErr(e.message));
  };
  useEffect(() => {
    if (status?.set_up) loadEntries();
  }, [status?.set_up]);

  // Gate an action behind unlock: open it now if we hold the key, else
  // stash it and show the unlock prompt.
  const gate = async (g: Gate) => {
    if (await hasKey()) {
      setUnlocked(true);
      setPanel(g);
    } else {
      setPending(g);
    }
  };

  // Bind the reveal panel to the URL (/vault/:id) so an entry is addressable —
  // you can link or email someone straight to it. Opening the URL gates the
  // reveal (prompting unlock if needed); navigating back closes it.
  useEffect(() => {
    if (!status?.set_up) return;
    if (detail.openId) {
      void gate({ kind: 'reveal', id: detail.openId });
    } else {
      setPanel((p) => (p?.kind === 'reveal' ? null : p));
    }
    // gate is recreated each render; we intentionally key only on the id + setup.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [detail.openId, status?.set_up]);

  // Tell the global chat launcher where we are. Only the entry *title* (a
  // non-secret summary field) is shared — decrypted secrets live solely in
  // RevealPanel/the worker and must never reach the agent.
  const openEntry = detail.openId ? entries?.find((e) => e.id === detail.openId) : null;
  useSetChatContext(
    detail.openId
      ? `the password vault, viewing entry ${openEntry?.title ? `"${openEntry.title}"` : `(id ${detail.openId})`}`
      : 'the password vault',
    loadEntries,
  );

  const shown = useMemo(() => {
    const needle = filter.trim().toLowerCase();
    const rows = (entries ?? []).slice().sort((a, b) => (a.title || '').localeCompare(b.title || ''));
    if (!needle) return rows;
    return rows.filter((r) =>
      `${r.title || ''} ${r.username || ''} ${r.url || ''} ${r.scope_summary || ''}`
        .toLowerCase()
        .includes(needle),
    );
  }, [entries, filter]);

  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <span>Vault</span>
        </nav>
        <div className="page-head-row">
          <h1>Vault</h1>
          {status?.set_up && (
            <div className="drawer-actions" style={{ margin: 0 }}>
              <button className="btn" onClick={() => gate({ kind: 'add' })}>
                Add secret
              </button>
              {me?.is_admin && (
                <button className="btn btn-ghost" onClick={() => setShowRotate(true)}>
                  Rotate password
                </button>
              )}
            </div>
          )}
        </div>
      </div>

      {err && <p className="banner banner-error">{err}</p>}
      {!status && !err && <p className="muted">Loading…</p>}

      {status && !status.set_up && (
        <SetupCard
          isAdmin={!!me?.is_admin}
          onSetup={() => {
            setShowSetup(false);
            setUnlocked(true);
            vaultApi<VaultStatus>('GET', '/status').then(setStatus);
          }}
          open={showSetup}
          setOpen={setShowSetup}
        />
      )}

      {status?.set_up && (
        <>
          <div className="toolbar">
            <input
              className="vault-filter"
              type="search"
              placeholder="Filter by title, username, URL…"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
            />
            {!unlocked && <span className="lock-hint">🔒 Locked — an action will prompt for the master password</span>}
          </div>
          {entries && shown.length === 0 ? (
            <p className="muted">No secrets you can view yet.</p>
          ) : (
            <ul className="entry-list">
              {shown.map((r) => (
                <li key={r.id}>
                  <button className="entry-link" onClick={() => detail.open(r.id)}>
                    <span className="entry-title">{r.title || '(untitled)'}</span>
                    <span className="entry-meta">
                      {[r.username, r.scope_summary].filter(Boolean).join(' — ')}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </>
      )}

      {pending && (
        <UnlockModal
          onClose={() => {
            setPending(null);
            detail.close();
          }}
          onUnlocked={() => {
            setUnlocked(true);
            const g = pending;
            setPending(null);
            setPanel(g);
          }}
        />
      )}

      {panel?.kind === 'add' && (
        <AddPanel
          onClose={() => setPanel(null)}
          onSaved={() => {
            setPanel(null);
            loadEntries();
          }}
        />
      )}
      {panel?.kind === 'reveal' && (
        <RevealPanel
          entryId={panel.id}
          onClose={() => {
            setPanel(null);
            detail.close();
          }}
          onSaved={loadEntries}
        />
      )}

      {showRotate && <RotateModal onClose={() => setShowRotate(false)} />}
    </div>
  );
}

function UnlockModal({ onClose, onUnlocked }: { onClose: () => void; onUnlocked: () => void }) {
  const [pw, setPw] = useState('');
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    try {
      await unlock(pw);
      onUnlocked();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="drawer-backdrop" onClick={onClose}>
      <aside className="drawer" onClick={(e) => e.stopPropagation()}>
        <button className="drawer-close" onClick={onClose} aria-label="Close">
          ×
        </button>
        <h2 className="panel-title">Unlock vault</h2>
        <p className="muted">Enter the shared master password your team uses.</p>
        {err && <p className="banner banner-error">{err}</p>}
        <form onSubmit={submit} className="stack-form">
          <label className="field">
            <span>Master password</span>
            <input
              type="password"
              autoFocus
              autoComplete="current-password"
              value={pw}
              onChange={(e) => setPw(e.target.value)}
            />
          </label>
          <div className="drawer-actions">
            <button className="btn" type="submit" disabled={busy}>
              {busy ? 'Unlocking…' : 'Unlock'}
            </button>
          </div>
        </form>
      </aside>
    </div>
  );
}

function SetupCard({
  isAdmin,
  open,
  setOpen,
  onSetup,
}: {
  isAdmin: boolean;
  open: boolean;
  setOpen: (v: boolean) => void;
  onSetup: () => void;
}) {
  const [pw, setPw] = useState(() => diceware(6));
  const [confirm, setConfirm] = useState('');
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  if (!isAdmin) {
    return <p className="muted">The vault isn't set up yet. Ask an admin to set it up.</p>;
  }
  if (!open) {
    return (
      <div className="panel">
        <h2 className="panel-title">Set up the vault</h2>
        <p className="status-line">
          Pick one shared master password for the team. Everyone types the same
          password to unlock — store it somewhere safe.
        </p>
        <button className="btn" onClick={() => setOpen(true)}>
          Set up vault
        </button>
      </div>
    );
  }
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (pw !== confirm) {
      setErr("Passwords don't match.");
      return;
    }
    if (pw.length < 4) {
      setErr('Pick at least 4 characters.');
      return;
    }
    setBusy(true);
    setErr(null);
    try {
      await setupVault(pw);
      onSetup();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="panel">
      <h2 className="panel-title">Set up the vault</h2>
      {err && <p className="banner banner-error">{err}</p>}
      <form onSubmit={submit} className="stack-form">
        <label className="field">
          <span>Master password (a diceware suggestion is pre-filled)</span>
          <input value={pw} onChange={(e) => setPw(e.target.value)} />
        </label>
        <label className="field">
          <span>Confirm</span>
          <input value={confirm} onChange={(e) => setConfirm(e.target.value)} />
        </label>
        <div className="drawer-actions">
          <button className="btn" type="submit" disabled={busy}>
            {busy ? 'Setting up…' : 'Create vault'}
          </button>
        </div>
      </form>
    </div>
  );
}

function RotateModal({ onClose }: { onClose: () => void }) {
  const [oldPw, setOldPw] = useState('');
  const [newPw, setNewPw] = useState(() => diceware(6));
  const [confirm, setConfirm] = useState('');
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (newPw !== confirm) {
      setErr("New passwords don't match.");
      return;
    }
    setBusy(true);
    setErr(null);
    try {
      await rotate(oldPw, newPw);
      setDone(true);
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="drawer-backdrop" onClick={onClose}>
      <aside className="drawer" onClick={(e) => e.stopPropagation()}>
        <button className="drawer-close" onClick={onClose} aria-label="Close">
          ×
        </button>
        <h2 className="panel-title">Rotate master password</h2>
        {done ? (
          <p className="banner banner-ok">Password rotated. Share the new one with the team.</p>
        ) : (
          <>
            {err && <p className="banner banner-error">{err}</p>}
            <form onSubmit={submit} className="stack-form">
              <label className="field">
                <span>Current password</span>
                <input type="password" value={oldPw} onChange={(e) => setOldPw(e.target.value)} />
              </label>
              <label className="field">
                <span>New password (diceware suggestion pre-filled)</span>
                <input value={newPw} onChange={(e) => setNewPw(e.target.value)} />
              </label>
              <label className="field">
                <span>Confirm new password</span>
                <input value={confirm} onChange={(e) => setConfirm(e.target.value)} />
              </label>
              <div className="drawer-actions">
                <button className="btn" type="submit" disabled={busy}>
                  {busy ? 'Rotating…' : 'Rotate password'}
                </button>
              </div>
            </form>
          </>
        )}
      </aside>
    </div>
  );
}
