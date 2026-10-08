import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  api,
  type CapabilityInfo,
  type Device,
  type DevicePreset,
  type DevicesView,
  type PendingPairing,
} from '../api';
import { useSetChatContext } from '../chatContext';
import { API_BASE } from '../api';

// Pair a device, and manage the ones already paired.
//
// Nobody signs in on a device. It opens /{slug}/pair and shows a picture and
// a code; the admin, on their own phone, taps the matching picture here (or
// types the code), picks a preset, names the device, and the device's next
// poll receives its session. A wrong tap cancels that pairing outright.

const POLL_MS = 3000;

function relative(iso: string | null): string {
  if (!iso) return 'never';
  const mins = Math.floor((Date.now() - new Date(iso).getTime()) / 60000);
  if (mins < 1) return 'just now';
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
}

interface Draft {
  preset: string;
  label: string;
  capabilities: string[];
}

function draftFor(preset: DevicePreset): Draft {
  return { preset: preset.key, label: preset.label, capabilities: [...preset.capabilities] };
}

// CapabilityPicker is the checklist shared by approval and editing.
function CapabilityPicker({
  all,
  value,
  onChange,
}: {
  all: CapabilityInfo[];
  value: string[];
  onChange: (next: string[]) => void;
}) {
  const toggle = (name: string) =>
    onChange(value.includes(name) ? value.filter((c) => c !== name) : [...value, name]);
  return (
    <div className="cap-list">
      {all.map((c) => (
        <label key={c.name}>
          <input type="checkbox" checked={value.includes(c.name)} onChange={() => toggle(c.name)} />
          {c.label}
        </label>
      ))}
    </div>
  );
}

// GrantForm is what the admin fills in once they've identified the device.
function GrantForm({
  view,
  draft,
  setDraft,
  busy,
  onSubmit,
  submitLabel,
}: {
  view: DevicesView;
  draft: Draft;
  setDraft: (d: Draft) => void;
  busy: boolean;
  onSubmit: () => void;
  submitLabel: string;
}) {
  const pick = (key: string) => {
    const p = view.presets.find((x) => x.key === key);
    if (p) setDraft({ ...draftFor(p), label: draft.label || p.label });
  };
  return (
    <form
      className="stack-form"
      onSubmit={(e) => {
        e.preventDefault();
        onSubmit();
      }}
    >
      <div className="field-row">
        <label className="field">
          Preset
          <select value={draft.preset} onChange={(e) => pick(e.target.value)}>
            {view.presets.map((p) => (
              <option key={p.key} value={p.key}>
                {p.label}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          Label
          <input
            required
            placeholder="Trivia laptop"
            value={draft.label}
            onChange={(e) => setDraft({ ...draft, label: e.target.value })}
          />
        </label>
      </div>
      <CapabilityPicker
        all={view.capabilities}
        value={draft.capabilities}
        onChange={(capabilities) => setDraft({ ...draft, capabilities })}
      />
      <button className="btn" type="submit" disabled={busy || draft.capabilities.length === 0}>
        {submitLabel}
      </button>
    </form>
  );
}

export default function Devices() {
  useSetChatContext('the Devices page (pairing shared machines)');
  const [view, setView] = useState<DevicesView | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // The pairing the admin has identified, by tapped picture or typed code.
  const [picked, setPicked] = useState<{ pairing?: PendingPairing; picture?: string; code?: string } | null>(null);
  const [code, setCode] = useState('');
  const [draft, setDraft] = useState<Draft | null>(null);
  const [editing, setEditing] = useState<Device | null>(null);
  const [editDraft, setEditDraft] = useState<Draft | null>(null);
  const [confirming, setConfirming] = useState<string | null>(null);

  const load = useCallback(() => {
    api
      .devices()
      .then((v) => {
        setView(v);
        setDraft((d) => d ?? (v.presets[0] ? draftFor(v.presets[0]) : null));
      })
      .catch((e) => setErr((e as Error).message));
  }, []);

  useEffect(() => {
    load();
    const t = setInterval(load, POLL_MS);
    return () => clearInterval(t);
  }, [load]);

  const approve = async () => {
    if (!picked || !draft) return;
    setBusy(true);
    setErr(null);
    try {
      await api.approvePairing({
        pairing_id: picked.pairing?.id,
        picture: picked.picture,
        code: picked.code,
        label: draft.label.trim(),
        capabilities: draft.capabilities,
      });
      setPicked(null);
      setCode('');
      load();
    } catch (e) {
      setErr((e as Error).message);
      setPicked(null);
      load();
    } finally {
      setBusy(false);
    }
  };

  const cancel = async (p: PendingPairing) => {
    setBusy(true);
    try {
      await api.cancelPairing(p.id);
      if (picked?.pairing?.id === p.id) setPicked(null);
      load();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const saveEdit = async () => {
    if (!editing || !editDraft) return;
    setBusy(true);
    setErr(null);
    try {
      await api.updateDevice(editing.id, {
        label: editDraft.label.trim(),
        capabilities: editDraft.capabilities,
      });
      setEditing(null);
      load();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const revoke = async (d: Device) => {
    setBusy(true);
    setErr(null);
    try {
      await api.revokeDevice(d.id);
      setConfirming(null);
      load();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const pairURL = view?.pair_url ?? '';
  const [copied, setCopied] = useState(false);
  const copyPairURL = () => {
    navigator.clipboard?.writeText(pairURL).then(
      () => {
        setCopied(true);
        setTimeout(() => setCopied(false), 1500);
      },
      () => setErr('Could not copy to the clipboard'),
    );
  };
  const capLabel = (name: string) => view?.capabilities.find((c) => c.name === name)?.label ?? name;
  const live = view?.devices.filter((d) => !d.revoked_at) ?? [];

  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <Link to="/admin">Admin</Link>
          <span className="crumb-sep">/</span>
          <span>Devices</span>
        </nav>
        <h1>Devices</h1>
        <p className="page-sub">
          Shared machines that nobody signs in on: the trivia laptop, the bar iPad, a phone
          for the night. On the device, scan this code or open the address. It shows a
          picture; tap the same picture below.
        </p>
      </div>

      {err && <p className="banner banner-error">{err}</p>}

      {view && (
        <section className="panel pair-address">
          <img className="pair-qr" src={`${API_BASE}/devices/pair.svg`} alt="QR code for the pairing address" width={200} height={200} />
          <div className="pair-address-text">
            <code>{pairURL}</code>
            <button className="btn" type="button" onClick={copyPairURL}>
              {copied ? 'Copied' : 'Copy'}
            </button>
          </div>
        </section>
      )}

      <section className="panel">
        <h2 className="panel-title">Waiting to pair</h2>
        {view && view.pending.length === 0 && !picked?.code ? (
          <p className="page-sub">No device is waiting. Open the pairing address on the device first.</p>
        ) : null}
        {view?.pending.map((p) => (
          <div key={p.id} className="card">
            <div className="card-main">
              <div className="card-title">Which picture is on the device?</div>
              <div className="pairing-pictures">
                {p.pictures.map((pic) => (
                  <button
                    key={pic}
                    type="button"
                    className={`pairing-picture${picked?.pairing?.id === p.id && picked.picture === pic ? ' selected' : ''}`}
                    disabled={busy}
                    onClick={() => setPicked({ pairing: p, picture: pic })}
                    aria-label={`picture ${pic}`}
                  >
                    {pic}
                  </button>
                ))}
              </div>
              <button className="btn btn-danger" type="button" disabled={busy} onClick={() => void cancel(p)}>
                Not a device I know
              </button>
            </div>
          </div>
        ))}
        <form
          className="stack-form"
          onSubmit={(e) => {
            e.preventDefault();
            if (code.trim()) setPicked({ code: code.trim().toUpperCase() });
          }}
        >
          <label className="field">
            Or type the code shown on the device
            <input
              placeholder="ABCD"
              value={code}
              maxLength={4}
              onChange={(e) => setCode(e.target.value)}
              style={{ textTransform: 'uppercase', letterSpacing: '0.2em' }}
            />
          </label>
          <button className="btn" type="submit" disabled={busy || code.trim().length !== 4}>
            Use code
          </button>
        </form>
        {picked && draft && view ? (
          <div className="card">
            <div className="card-main">
              <div className="card-title">
                {picked.code ? `Device with code ${picked.code}` : `Device showing ${picked.picture}`}
              </div>
              <GrantForm
                view={view}
                draft={draft}
                setDraft={setDraft}
                busy={busy}
                onSubmit={() => void approve()}
                submitLabel="Pair this device"
              />
            </div>
          </div>
        ) : null}
      </section>

      <section className="panel">
        <h2 className="panel-title">Paired devices</h2>
        {view && live.length === 0 ? <p className="page-sub">No devices yet.</p> : null}
        <ul className="card-list">
          {live.map((d) => (
            <li key={d.id} className="card">
              <div className="card-main">
                <div className="card-title">{d.label}</div>
                {editing?.id === d.id && editDraft && view ? (
                  <>
                    <GrantForm
                      view={view}
                      draft={editDraft}
                      setDraft={setEditDraft}
                      busy={busy}
                      onSubmit={() => void saveEdit()}
                      submitLabel="Save"
                    />
                    <button className="btn" type="button" disabled={busy} onClick={() => setEditing(null)}>
                      Cancel
                    </button>
                  </>
                ) : (
                  <>
                    <div className="chip-row">
                      {d.capabilities.map((c) => (
                        <span key={c} className="chip">
                          {capLabel(c)}
                        </span>
                      ))}
                    </div>
                    <p className="page-sub">Last seen {relative(d.last_seen_at)}</p>
                    <div className="page-head-actions">
                      <button
                        className="btn"
                        type="button"
                        disabled={busy}
                        onClick={() => {
                          setEditing(d);
                          setEditDraft({ preset: '', label: d.label, capabilities: [...d.capabilities] });
                        }}
                      >
                        Edit
                      </button>
                      {confirming === d.id ? (
                        <>
                          <button className="btn btn-danger" type="button" disabled={busy} onClick={() => void revoke(d)}>
                            Really unpair
                          </button>
                          <button className="btn" type="button" disabled={busy} onClick={() => setConfirming(null)}>
                            Keep
                          </button>
                        </>
                      ) : (
                        <button className="btn btn-danger" type="button" disabled={busy} onClick={() => setConfirming(d.id)}>
                          Unpair
                        </button>
                      )}
                    </div>
                  </>
                )}
              </div>
            </li>
          ))}
        </ul>
      </section>
    </div>
  );
}
