import { useEffect, useMemo, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { useMe } from '../me';
import { SLUG } from '../workspace';
import { vaultApi, type VaultEntrySummary, type VaultStatus } from './vault/api';
import { diceware } from './vault/crypto';
import { connectWorker, hasKey, nukeVault, rotate, setupVault, unlock } from './vault/worker';
import { AddPanel, RevealPanel } from './vault/panels';
import { useDetailRoute } from '../useDetailRoute';
import { useSetChatContext } from '../chatContext';
import ActionMenu from '../ActionMenu';

// revealErrorCopy maps the reason code carried on the ?reveal_error query
// (set by the Slack reveal bridge when a one-shot token is rejected) to
// user-facing copy. The bridge consumes the token server-side then lands
// the user here, so this is the only place that explains why the tap
// didn't open the secret.
function revealErrorCopy(reason: string): string {
  switch (reason) {
    case 'expired':
      return 'That reveal link expired — they are only valid for 2 minutes. Ask Kit to reveal the secret again.';
    case 'consumed':
      return 'That reveal link was already used. Each link works only once — ask Kit for a fresh one.';
    case 'entry_mismatch':
    case 'tenant_mismatch':
      return 'That reveal link pointed somewhere unexpected. Ask Kit for a fresh link.';
    default:
      return 'That reveal link is no longer valid. Ask Kit for a fresh link.';
  }
}

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
  const [showNuke, setShowNuke] = useState(false);

  // A rejected Slack reveal link lands here with ?reveal_error=<reason>.
  // Surface it as a dismissible banner, then strip the param so a refresh
  // doesn't re-show it.
  const [params, setParams] = useSearchParams();
  const [revealError, setRevealError] = useState<string | null>(null);
  useEffect(() => {
    const reason = params.get('reveal_error');
    if (!reason) return;
    setRevealError(revealErrorCopy(reason));
    params.delete('reveal_error');
    setParams(params, { replace: true });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

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
            <div className="page-head-actions">
              <button className="btn" onClick={() => gate({ kind: 'add' })}>
                Add secret
              </button>
              {me?.is_admin && (
                <ActionMenu
                  label="Vault settings"
                  items={[
                    { label: 'Rotate password', onClick: () => setShowRotate(true) },
                    { label: 'Destroy vault', onClick: () => setShowNuke(true), danger: true },
                  ]}
                />
              )}
            </div>
          )}
        </div>
      </div>

      {revealError && (
        <p className="banner banner-error" role="alert">
          {revealError}{' '}
          <button className="btn btn-ghost" onClick={() => setRevealError(null)}>
            Dismiss
          </button>
        </p>
      )}
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
      {showNuke && (
        <NukeModal
          entryCount={entries?.length ?? 0}
          onClose={() => setShowNuke(false)}
          onNuked={() => {
            setShowNuke(false);
            setUnlocked(false);
            setEntries(null);
            setPanel(null);
            vaultApi<VaultStatus>('GET', '/status').then(setStatus);
          }}
        />
      )}
    </div>
  );
}

function UnlockModal({ onClose, onUnlocked }: { onClose: () => void; onUnlocked: () => void }) {
  const [pw, setPw] = useState('');
  const [showPw, setShowPw] = useState(false);
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
        {/* autoComplete="off" + a non-login field name keep the browser's
            password manager from autofilling this shared master-password box
            with the user's saved site/login credential — a silent wrong value
            that reads as "incorrect password". The show toggle lets the user
            confirm exactly what will be submitted. */}
        <form onSubmit={submit} className="stack-form" autoComplete="off">
          <label className="field">
            <span>Master password</span>
            <input
              type={showPw ? 'text' : 'password'}
              name="vault-master-password"
              autoFocus
              autoComplete="off"
              autoCorrect="off"
              autoCapitalize="off"
              spellCheck={false}
              value={pw}
              onChange={(e) => setPw(e.target.value)}
            />
          </label>
          <label style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
            <input type="checkbox" checked={showPw} onChange={(e) => setShowPw(e.target.checked)} />
            <span>Show password</span>
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

// NukeModal — admin-only destruction of the whole vault. The server gates
// on confirm_slug === workspace slug and admin; we mirror that gate in the
// UI by requiring the slug to be typed before enabling the button. There
// is no undo, so the copy leans hard on that.
function NukeModal({
  entryCount,
  onClose,
  onNuked,
}: {
  entryCount: number;
  onClose: () => void;
  onNuked: () => void;
}) {
  const [confirm, setConfirm] = useState('');
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const matches = confirm === SLUG;
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!matches) return;
    setBusy(true);
    setErr(null);
    try {
      await nukeVault(confirm);
      onNuked();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const plural = entryCount === 1 ? '' : 's';
  return (
    <div className="drawer-backdrop" onClick={onClose}>
      <aside className="drawer" onClick={(e) => e.stopPropagation()}>
        <button className="drawer-close" onClick={onClose} aria-label="Close">
          ×
        </button>
        <h2 className="panel-title">Destroy the vault</h2>
        <p className="banner banner-error">
          This permanently deletes all {entryCount} secret{plural}. They cannot be recovered —
          not even with the master password. Use this only if the master password is lost and the
          team is starting over from an empty vault.
        </p>
        {err && <p className="banner banner-error">{err}</p>}
        <form onSubmit={submit} className="stack-form">
          <label className="field">
            <span>
              Type your workspace slug <code>{SLUG}</code> to confirm
            </span>
            <input value={confirm} autoComplete="off" onChange={(e) => setConfirm(e.target.value)} />
          </label>
          <div className="drawer-actions">
            <button className="btn btn-danger" type="submit" disabled={busy || !matches}>
              {busy ? 'Destroying…' : `Destroy ${entryCount} secret${plural} permanently`}
            </button>
          </div>
        </form>
      </aside>
    </div>
  );
}
