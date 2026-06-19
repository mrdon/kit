import { useCallback, useEffect, useRef, useState } from 'react';
import { vaultApi, type Principal, type VaultEntryFull } from './api';
import { b64ToBytes, bytesToB64, generatePassword, normalizeURL } from './crypto';
import { decryptEntry, encryptEntry } from './worker';
import { compactTOTP, expandTOTP, generateTOTP, parseOtpauthURI, type TotpParams } from './totp';
import { VisibilityEditor } from './VisibilityEditor';

// SecretValues is the editable shape shared by the add and edit forms.
// password/notes/totp live inside the encrypted blob; title/username/url
// are plaintext metadata.
interface SecretValues {
  title: string;
  username: string;
  url: string;
  password: string;
  notes: string;
  totp: string;
}

const EMPTY_VALUES: SecretValues = {
  title: '',
  username: '',
  url: '',
  password: '',
  notes: '',
  totp: '',
};

// encryptValueFields re-encrypts the secret blob ({password, notes, totp})
// and returns the base64 ciphertext/nonce the API expects. The plaintext
// never leaves the worker boundary in a form the server can read.
async function encryptValueFields(v: SecretValues): Promise<{ value_ciphertext: string; value_nonce: string }> {
  const value: Record<string, unknown> = { password: v.password, notes: v.notes };
  const parsed = parseOtpauthURI(v.totp);
  if (parsed) value.totp = compactTOTP(parsed);
  const enc = await encryptEntry(new TextEncoder().encode(JSON.stringify(value)));
  return { value_ciphertext: bytesToB64(enc.ciphertext), value_nonce: bytesToB64(enc.nonce) };
}

// totpToInput renders a stored (compact) TOTP back into an editable string:
// the bare base32 secret when params are default, else a full otpauth URI so
// non-default algorithm/digits/period survive a round-trip.
function totpToInput(t?: Partial<TotpParams>): string {
  if (!t || !t.secret) return '';
  const full = expandTOTP(t);
  if (full.algorithm === 'SHA1' && full.digits === 6 && full.period === 30) return full.secret;
  const p = new URLSearchParams({
    secret: full.secret,
    algorithm: full.algorithm,
    digits: String(full.digits),
    period: String(full.period),
  });
  return `otpauth://totp/Kit?${p.toString()}`;
}

// SecretFields renders the editable inputs shared by the add and edit
// forms. roleSlot is add-only (re-scoping has its own endpoint) and renders
// right after the title.
function SecretFields({
  values,
  onChange,
  roleSlot,
}: {
  values: SecretValues;
  onChange: (patch: Partial<SecretValues>) => void;
  roleSlot?: React.ReactNode;
}) {
  const [showPw, setShowPw] = useState(false);
  return (
    <>
      <label className="field">
        <span>Title</span>
        <input required value={values.title} onChange={(e) => onChange({ title: e.target.value })} />
      </label>
      {roleSlot}
      <div className="field-row">
        <label className="field">
          <span>Username</span>
          <input value={values.username} onChange={(e) => onChange({ username: e.target.value })} />
        </label>
        <label className="field">
          <span>URL</span>
          <input value={values.url} onChange={(e) => onChange({ url: e.target.value })} />
        </label>
      </div>
      <label className="field">
        <span>Password</span>
        <div className="field-row">
          <input
            type={showPw ? 'text' : 'password'}
            value={values.password}
            onChange={(e) => onChange({ password: e.target.value })}
          />
          <button type="button" className="btn btn-ghost" onClick={() => setShowPw((s) => !s)}>
            {showPw ? 'Hide' : 'Show'}
          </button>
          <button
            type="button"
            className="btn btn-ghost"
            onClick={() => {
              onChange({ password: generatePassword(20) });
              setShowPw(true);
            }}
          >
            Generate
          </button>
        </div>
      </label>
      <label className="field">
        <span>TOTP (otpauth:// URI or base32 secret)</span>
        <input value={values.totp} onChange={(e) => onChange({ totp: e.target.value })} />
      </label>
      <label className="field">
        <span>Notes</span>
        <textarea rows={3} value={values.notes} onChange={(e) => onChange({ notes: e.target.value })} />
      </label>
    </>
  );
}

// AddPanel — encrypt a new entry via the worker and POST it.
export function AddPanel({ onClose, onSaved }: { onClose: () => void; onSaved: () => void }) {
  const [roles, setRoles] = useState<Principal[]>([]);
  const [roleId, setRoleId] = useState('');
  const [values, setValues] = useState<SecretValues>(EMPTY_VALUES);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const set = (patch: Partial<SecretValues>) => setValues((v) => ({ ...v, ...patch }));

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
      const enc = await encryptValueFields(values);
      await vaultApi('POST', '/entries', {
        title: values.title,
        username: values.username,
        url: normalizeURL(values.url),
        ...enc,
        role_id: roleId,
      });
      onSaved();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const roleSlot = (
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
  );

  return (
    <div className="drawer-backdrop" onClick={onClose}>
      <aside className="drawer" onClick={(e) => e.stopPropagation()}>
        <button className="drawer-close" onClick={onClose} aria-label="Close">
          ×
        </button>
        <h2 className="panel-title">Add a secret</h2>
        {err && <p className="banner banner-error">{err}</p>}
        <form onSubmit={submit} className="stack-form">
          <SecretFields values={values} onChange={set} roleSlot={roleSlot} />
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

type DecodedValue = { password?: string; notes?: string; totp?: Partial<TotpParams> };

// RevealPanel — fetch + decrypt an entry (worker must already hold the key),
// then either display it read-only or flip into an inline edit form.
export function RevealPanel({
  entryId,
  onClose,
  onSaved,
}: {
  entryId: string;
  onClose: () => void;
  onSaved?: () => void;
}) {
  const [entry, setEntry] = useState<VaultEntryFull | null>(null);
  const [decoded, setDecoded] = useState<DecodedValue | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [showSecret, setShowSecret] = useState(false);
  const [editing, setEditing] = useState(false);

  const load = useCallback(async () => {
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
  }, [entryId]);

  useEffect(() => {
    load();
  }, [load]);

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
        {decoded && editing && entry && (
          <EditForm
            entryId={entryId}
            initial={{
              title,
              username: username || '',
              url: url || '',
              password: decoded.password || '',
              notes: decoded.notes || '',
              totp: totpToInput(decoded.totp),
            }}
            tags={entry.Tags || entry.tags || []}
            onCancel={() => setEditing(false)}
            onSaved={async () => {
              setEditing(false);
              await load();
              onSaved?.();
            }}
          />
        )}
        {decoded && !editing && (
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
            <VisibilityEditor
              entryId={entryId}
              roleId={entry?.role_id}
              roleName={entry?.role_name}
              onChanged={async () => {
                await load();
                onSaved?.();
              }}
            />
            <div className="drawer-actions">
              <button className="btn" onClick={() => setEditing(true)}>
                Edit
              </button>
            </div>
          </>
        )}
      </aside>
    </div>
  );
}

// EditForm re-encrypts the secret blob and PUTs the entry. Tags are passed
// through verbatim — the API rewrites them on every update, so dropping them
// here would wipe tags set via the agent/MCP surface.
function EditForm({
  entryId,
  initial,
  tags,
  onCancel,
  onSaved,
}: {
  entryId: string;
  initial: SecretValues;
  tags: string[];
  onCancel: () => void;
  onSaved: () => void;
}) {
  const [values, setValues] = useState<SecretValues>(initial);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const set = (patch: Partial<SecretValues>) => setValues((v) => ({ ...v, ...patch }));

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    try {
      const enc = await encryptValueFields(values);
      await vaultApi('PUT', `/entries/${entryId}`, {
        title: values.title,
        username: values.username,
        url: normalizeURL(values.url),
        tags,
        ...enc,
      });
      onSaved();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <h2 className="panel-title">Edit secret</h2>
      {err && <p className="banner banner-error">{err}</p>}
      <form onSubmit={submit} className="stack-form">
        <SecretFields values={values} onChange={set} />
        <div className="drawer-actions">
          <button className="btn" type="submit" disabled={busy}>
            {busy ? 'Encrypting…' : 'Save changes'}
          </button>
          <button className="btn btn-ghost" type="button" onClick={onCancel} disabled={busy}>
            Cancel
          </button>
        </div>
      </form>
    </>
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
