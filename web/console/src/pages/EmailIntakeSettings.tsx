import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api } from '../api';
import { useSetChatContext } from '../chatContext';

// Per-user page: enable and tune email→task intake. Scanning your mailbox and
// creating tasks is opt-in, so nothing runs until you turn it on here.
export default function EmailIntakeSettings() {
  useSetChatContext('the Email intake settings page (email→task scanning)');
  const [loaded, setLoaded] = useState(false);
  const [enabled, setEnabled] = useState(false);
  const [schedule, setSchedule] = useState('0 7 * * *');
  const [extra, setExtra] = useState('');
  const [lastScan, setLastScan] = useState<string | null>(null);
  const [hasMailbox, setHasMailbox] = useState(true);
  const [defaultText, setDefaultText] = useState('');

  const [err, setErr] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api
      .emailIntake()
      .then((r) => {
        setEnabled(r.enabled);
        setSchedule(r.schedule);
        setExtra(r.extra_instructions);
        setLastScan(r.last_scanned_at);
        setHasMailbox(r.has_mailbox);
        setDefaultText(r.default_instructions);
        setLoaded(true);
      })
      .catch((e) => setErr(e.message));
  }, []);

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    setSaved(false);
    try {
      const r = await api.setEmailIntake({
        enabled,
        schedule,
        extra_instructions: extra,
      });
      setLastScan(r.last_scanned_at);
      setSaved(true);
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <Link to="/tasks">Tasks</Link>
          <span className="crumb-sep">/</span>
          <span>Email intake</span>
        </nav>
        <h1>Email intake</h1>
        <p className="page-sub">
          Kit scans your connected mailbox on a schedule and turns follow-ups
          into tasks — skipping receipts and anything already tracked. It reads
          only mail newer than the last scan, and never sends or deletes
          anything.
        </p>
      </div>

      {err && <p className="banner banner-error">{err}</p>}
      {saved && <p className="banner banner-ok">Saved.</p>}

      {loaded && !hasMailbox && (
        <p className="banner banner-error" style={{ maxWidth: '32rem' }}>
          You haven’t connected a mailbox yet. Add an email integration under{' '}
          <Link to="/admin/integrations">Integrations</Link> first, then enable
          intake here.
        </p>
      )}

      <form onSubmit={save} className="stack-form" style={{ maxWidth: '32rem' }}>
        <label className="switch">
          <input
            type="checkbox"
            checked={enabled}
            onChange={(e) => setEnabled(e.target.checked)}
          />
          <span className="switch-track" aria-hidden="true" />
          <span>Scan my email for tasks</span>
        </label>

        <label className="field">
          <span>Schedule (cron)</span>
          <input
            type="text"
            value={schedule}
            onChange={(e) => setSchedule(e.target.value)}
            placeholder="0 7 * * *"
          />
          <small className="page-sub">
            When to scan, in cron syntax. Default <code>0 7 * * *</code> is every
            day at 7am.
          </small>
        </label>

        <label className="field">
          <span>Extra instructions (optional)</span>
          <textarea
            rows={4}
            value={extra}
            onChange={(e) => setExtra(e.target.value)}
            placeholder="e.g. Also scan my Newsletters folder. Ignore anything from calendar@."
          />
          <small className="page-sub">
            Added on top of Kit’s built-in triage rules — you can add to them,
            but not remove them.
          </small>
        </label>

        {lastScan && (
          <p className="page-sub">
            Last scanned: {new Date(lastScan).toLocaleString()}
          </p>
        )}

        <div className="drawer-actions">
          <button className="btn" type="submit" disabled={busy}>
            {busy ? 'Saving…' : 'Save'}
          </button>
        </div>
      </form>

      {defaultText && (
        <details style={{ marginTop: '1.5rem', maxWidth: '48rem' }}>
          <summary className="page-sub">
            View Kit’s built-in triage instructions
          </summary>
          <pre
            style={{
              whiteSpace: 'pre-wrap',
              fontSize: '0.8rem',
              opacity: 0.85,
              marginTop: '0.75rem',
            }}
          >
            {defaultText}
          </pre>
        </details>
      )}
    </div>
  );
}
