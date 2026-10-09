import { useState } from 'react';
import { api, type KioskDestination } from '../api';

// The workspace's own screen buttons: an outside page staff switch a screen
// to often enough that typing its address each time is the wrong interface.
// Menu, Events and Trivia are built in and not listed here. Removing a button
// never changes what a screen shows; it only takes the shortcut away.

interface Props {
  destinations: KioskDestination[];
  onChange: () => void;
  onError: (msg: string) => void;
}

export default function KioskButtons({ destinations, onChange, onError }: Props) {
  const custom = destinations.filter((d) => d.custom);
  const [label, setLabel] = useState('');
  const [url, setUrl] = useState('');
  const [busy, setBusy] = useState(false);

  const add = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      await api.createKioskButton({ label: label.trim(), url: url.trim() });
      setLabel('');
      setUrl('');
      onChange();
    } catch (err) {
      onError((err as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const remove = async (d: KioskDestination) => {
    setBusy(true);
    try {
      await api.deleteKioskButton(d.key);
      onChange();
    } catch (err) {
      onError((err as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="panel">
      <h2 className="panel-title">Screen buttons</h2>
      <p className="muted">
        Menu, Events and Trivia are always there. Add a button for any other page
        your screens switch to often, and it shows up next to them here and on
        the bar iPad.
      </p>
      {custom.length > 0 && (
        <ul className="kiosk-buttons">
          {custom.map((d) => (
            <li key={d.key}>
              <strong>{d.label}</strong>
              <code title={d.url}>{d.url}</code>
              <button className="btn btn-sm btn-ghost" type="button" disabled={busy} onClick={() => remove(d)}>
                Remove
              </button>
            </li>
          ))}
        </ul>
      )}
      <form className="stack-form" onSubmit={add}>
        <div className="field-row">
          <label className="field">
            Button name
            <input required maxLength={30} placeholder="Untappd" value={label} onChange={(e) => setLabel(e.target.value)} />
          </label>
          <label className="field">
            Shows
            <input
              required
              type="url"
              placeholder="https://example.com/board"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
            />
          </label>
        </div>
        <div>
          <button className="btn" type="submit" disabled={busy}>
            Add button
          </button>
        </div>
      </form>
    </section>
  );
}
