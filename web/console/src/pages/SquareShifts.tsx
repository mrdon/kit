import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type SquareShiftsStatus, type SquareShiftRun } from '../api';
import { useSetChatContext } from '../chatContext';

export default function SquareShifts() {
  useSetChatContext('the admin Square Shift Sync page');
  const [st, setSt] = useState<SquareShiftsStatus | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const load = () => {
    api
      .squareShiftsStatus()
      .then(setSt)
      .catch((e) => setErr(e.message));
  };
  useEffect(load, []);

  const syncNow = async () => {
    setBusy(true);
    setErr(null);
    setNote(null);
    try {
      const next = await api.squareShiftsSync();
      setSt(next);
      const last = next.recent[0];
      setNote(
        last
          ? `Sync complete: ${last.created} created, ${last.updated} updated, ${last.deleted} deleted.`
          : 'Sync complete.',
      );
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const ready = st?.square_connected && st?.google_connected;

  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <Link to="/admin">Admin</Link>
          <span className="crumb-sep">/</span>
          <span>Square Shift Sync</span>
        </nav>
        <h1>Square Shift Sync</h1>
        <p className="page-sub">
          Mirror your published Square staff schedule into a Google Calendar your
          team subscribes to. Runs automatically every 15 minutes.
        </p>
      </div>

      {note && (
        <p className="banner banner-ok" onClick={() => setNote(null)}>
          {note}
        </p>
      )}
      {err && <p className="banner banner-error">{err}</p>}
      {!st && !err && <p className="muted">Loading…</p>}

      {st && (
        <>
          <section className="panel">
            <h2 className="panel-title">Connections</h2>
            <p className="status-line">
              {st.square_connected ? (
                <span className="pill pill-ok">Square connected</span>
              ) : (
                <span className="pill pill-off">Square not connected</span>
              )}{' '}
              {st.google_connected ? (
                <span className="pill pill-ok">Google Calendar connected</span>
              ) : (
                <span className="pill pill-off">Google Calendar not connected</span>
              )}
            </p>
            {!ready && (
              <p className="muted">
                Connect both on the{' '}
                <Link to="/admin/integrations">Integrations</Link> page to enable
                syncing.
              </p>
            )}
            {ready && !st.enabled && (
              <p className="muted">
                Both connected, but the feature is turned off. Enable{' '}
                <strong>Square Shift Sync</strong> on the{' '}
                <Link to="/admin/apps">Apps</Link> page.
              </p>
            )}
          </section>

          <section className="panel">
            <h2 className="panel-title">Sync</h2>
            <button className="btn" onClick={syncNow} disabled={busy || !ready}>
              {busy ? 'Syncing…' : 'Sync now'}
            </button>
            <RunHistory runs={st.recent} />
          </section>
        </>
      )}
    </div>
  );
}

function RunHistory({ runs }: { runs: SquareShiftRun[] }) {
  if (runs.length === 0) {
    return <p className="muted">No sync has run yet.</p>;
  }
  return (
    <table className="item-table">
      <thead>
        <tr>
          <th>When</th>
          <th>Result</th>
          <th>Changes</th>
          <th>Trigger</th>
        </tr>
      </thead>
      <tbody>
        {runs.map((r, i) => (
          <tr key={i}>
            <td>{new Date(r.at).toLocaleString()}</td>
            <td>
              {r.status === 'failed' ? (
                <span className="pill pill-off">Failed</span>
              ) : (
                <span className="pill pill-ok">OK</span>
              )}
            </td>
            <td>
              {r.status === 'failed'
                ? r.error
                : `${r.created} created, ${r.updated} updated, ${r.deleted} deleted`}
            </td>
            <td>{r.triggered_by}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
