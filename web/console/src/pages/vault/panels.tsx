import { useEffect, useRef, useState } from 'react';
import { vaultApi, type Principal, type VaultEntryFull } from './api';
import { b64ToBytes, bytesToB64, generatePassword, normalizeURL } from './crypto';
import { decryptEntry, encryptEntry } from './worker';
import { compactTOTP, expandTOTP, generateTOTP, parseOtpauthURI, type TotpParams } from './totp';

// AddPanel — encrypt a new entry via the worker and POST it.
export function AddPanel({ onClose, onSaved }: { onClose: () => void; onSaved: () => void }) {
  const [roles, setRoles] = useState<Principal[]>([]);
  const [roleId, setRoleId] = useState('');
  const [title, setTitle] = useState('');
  const [username, setUsername] = useState('');
  const [url, setUrl] = useState('');
  const [password, setPassword] = useState('');
  const [showPw, setShowPw] = useState(false);
  const [notes, setNotes] = useState('');
  const [totp, setTotp] = useState('');
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    vaultApi<{ roles: Principal[]; default_role_id?: string }>('GET', '/principals')
      .then((d) => {
        setRoles(d.roles || []);
        if (d.default_role_id) setRoleId(d.default_role_id);
      })
      .catch((e) => setErr(e.message));
  }, []);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!roleId) {
      setErr('Pick a role.');
      return;
    }
    setBusy(true);
    setErr(null);
    try {
      const value: Record<string, unknown> = { password, notes };
      const parsed = parseOtpauthURI(totp);
      if (parsed) value.totp = compactTOTP(parsed);
      const enc = await encryptEntry(new TextEncoder().encode(JSON.stringify(value)));
      await vaultApi('POST', '/entries', {
        title,
        username,
        url: normalizeURL(url),
        value_ciphertext: bytesToB64(enc.ciphertext),
        value_nonce: bytesToB64(enc.nonce),
        role_id: roleId,
      });
      onSaved();
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
        <h2 className="panel-title">Add a secret</h2>
        {err && <p className="banner banner-error">{err}</p>}
        <form onSubmit={submit} className="stack-form">
          <label className="field">
            <span>Title</span>
            <input required value={title} onChange={(e) => setTitle(e.target.value)} />
          </label>
          <label className="field">
            <span>Who can see it</span>
            <select value={roleId} onChange={(e) => setRoleId(e.target.value)}>
              {roles.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.name === 'member' ? 'Everyone (members)' : r.name}
                </option>
              ))}
            </select>
          </label>
          <div className="field-row">
            <label className="field">
              <span>Username</span>
              <input value={username} onChange={(e) => setUsername(e.target.value)} />
            </label>
            <label className="field">
              <span>URL</span>
              <input value={url} onChange={(e) => setUrl(e.target.value)} />
            </label>
          </div>
          <label className="field">
            <span>Password</span>
            <div className="field-row">
              <input
                type={showPw ? 'text' : 'password'}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
              <button type="button" className="btn btn-ghost" onClick={() => setShowPw((s) => !s)}>
                {showPw ? 'Hide' : 'Show'}
              </button>
              <button
                type="button"
                className="btn btn-ghost"
                onClick={() => {
                  setPassword(generatePassword(20));
                  setShowPw(true);
                }}
              >
                Generate
              </button>
            </div>
          </label>
          <label className="field">
            <span>TOTP (otpauth:// URI or base32 secret)</span>
            <input value={totp} onChange={(e) => setTotp(e.target.value)} />
          </label>
          <label className="field">
            <span>Notes</span>
            <textarea rows={3} value={notes} onChange={(e) => setNotes(e.target.value)} />
          </label>
          <div className="drawer-actions">
            <button className="btn" type="submit" disabled={busy}>
              {busy ? 'Encrypting…' : 'Save secret'}
            </button>
          </div>
        </form>
      </aside>
    </div>
  );
}

// RevealPanel — fetch + decrypt an entry (worker must already hold the key).
export function RevealPanel({ entryId, onClose }: { entryId: string; onClose: () => void }) {
  const [entry, setEntry] = useState<VaultEntryFull | null>(null);
  const [decoded, setDecoded] = useState<{ password?: string; notes?: string; totp?: Partial<TotpParams> } | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [showSecret, setShowSecret] = useState(false);

  useEffect(() => {
    (async () => {
      try {
        const e = await vaultApi<VaultEntryFull>('GET', `/entries/${entryId}`);
        setEntry(e);
        const ct = b64ToBytes((e.ValueCiphertext || e.value_ciphertext)!);
        const nonce = b64ToBytes((e.ValueNonce || e.value_nonce)!);
        const pt = await decryptEntry(ct, nonce);
        setDecoded(JSON.parse(new TextDecoder().decode(pt)));
      } catch (e) {
        setErr((e as Error).message);
      }
    })();
  }, [entryId]);

  const title = entry?.Title || entry?.title || '(untitled)';
  const username = entry?.Username || entry?.username;
  const url = entry?.URL || entry?.url;

  return (
    <div className="drawer-backdrop" onClick={onClose}>
      <aside className="drawer" onClick={(e) => e.stopPropagation()}>
        <button className="drawer-close" onClick={onClose} aria-label="Close">
          ×
        </button>
        {err && <p className="banner banner-error">{err}</p>}
        {!decoded && !err && <p className="muted">Decrypting…</p>}
        {decoded && (
          <>
            <h2 className="panel-title">{title}</h2>
            <dl className="entry-fields">
              {username && (
                <>
                  <dt>Username</dt>
                  <dd>{username}</dd>
                </>
              )}
              {decoded.password && (
                <>
                  <dt>Password</dt>
                  <dd className="secret-row">
                    <span className="secret-value">
                      {showSecret ? decoded.password : '•••••'}
                    </span>
                    <button className="btn btn-ghost" onClick={() => setShowSecret((s) => !s)}>
                      {showSecret ? 'Hide' : 'Show'}
                    </button>
                    <button
                      className="btn btn-ghost"
                      onClick={() => navigator.clipboard.writeText(decoded.password!)}
                    >
                      Copy
                    </button>
                  </dd>
                </>
              )}
              {url && (
                <>
                  <dt>URL</dt>
                  <dd>
                    <a href={url} target="_blank" rel="noopener noreferrer">
                      {url}
                    </a>
                  </dd>
                </>
              )}
              {decoded.notes && (
                <>
                  <dt>Notes</dt>
                  <dd>{decoded.notes}</dd>
                </>
              )}
            </dl>
            {decoded.totp && <TotpDisplay params={expandTOTP(decoded.totp)} />}
          </>
        )}
      </aside>
    </div>
  );
}

function TotpDisplay({ params }: { params: TotpParams }) {
  const [code, setCode] = useState('……');
  const timer = useRef<number | null>(null);
  useEffect(() => {
    const tick = async () => {
      try {
        const { code } = await generateTOTP(params, Date.now());
        setCode(code);
      } catch {
        setCode('(TOTP error)');
      }
    };
    tick();
    timer.current = window.setInterval(tick, 1000);
    return () => {
      if (timer.current) clearInterval(timer.current);
    };
  }, [params]);
  return (
    <div className="totp-box">
      <span className="totp-label">TOTP</span>
      <span className="totp-code">{code}</span>
    </div>
  );
}
