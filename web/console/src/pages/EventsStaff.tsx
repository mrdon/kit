import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  api,
  type EventsNoticePlan,
  type EventsNoticeRun,
  type EventsStaff,
} from '../api';
import { useSetChatContext } from '../chatContext';

// Admin page for shift notices: pair each person on the Square schedule with
// the Slack account Kit should DM about the events on their shifts.
//
// Both sides are opaque ids — Square's TM… and Slack's U… — so this page never
// shows one. Two name-labelled dropdowns, and the ids stay behind them.

export default function EventsStaffPage() {
  useSetChatContext('the admin Events staff notices page');
  const [st, setSt] = useState<EventsStaff | null>(null);
  const [plans, setPlans] = useState<EventsNoticePlan[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const load = () => {
    api
      .eventsStaff()
      .then(setSt)
      .catch((e) => setErr((e as Error).message));
  };
  useEffect(load, []);

  const run = async (fn: () => Promise<void>) => {
    setBusy(true);
    setErr(null);
    setNote(null);
    try {
      await fn();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const setMapping = (teamMemberID: string, slackUserID: string) =>
    run(async () => {
      setSt(
        await api.saveEventsStaffMapping({
          square_team_member_id: teamMemberID,
          slack_user_id: slackUserID,
        }),
      );
      // A changed mapping changes who would receive what, so a preview shown
      // against the old pairing is worse than none.
      setPlans(null);
      setNote(slackUserID ? 'Mapping saved.' : 'Mapping cleared.');
    });

  const preview = () =>
    run(async () => {
      const r = await api.previewEventsNotices();
      setPlans(r.plans ?? []);
    });

  const send = () =>
    run(async () => {
      const r = await api.sendEventsNotices();
      setSt(r.staff);
      setPlans(null);
      setNote(r.message);
    });

  const staff = st?.staff ?? [];
  const slackUsers = st?.slack_users ?? [];
  const mappings = st?.mappings ?? [];
  const recent = st?.recent ?? [];

  const mappedFor = (teamMemberID: string) =>
    mappings.find((m) => m.square_team_member_id === teamMemberID)
      ?.slack_user_id ?? '';

  const unmapped = staff.filter((s) => !mappedFor(s.team_member_id)).length;

  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <Link to="/admin">Admin</Link>
          <span className="crumb-sep">/</span>
          <span>Event staff notices</span>
        </nav>
        <h1>Event staff notices</h1>
        <p className="page-sub">
          Each morning at 8am, everyone working that day gets a DM listing
          what&rsquo;s on — private bookings included. Pair each person on the
          Square schedule with their Slack account so Kit knows who to message.
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
            <h2 className="panel-title">Who gets notified</h2>
            {st.staff_error && <p className="muted">{st.staff_error}</p>}
            {st.slack_error && <p className="muted">{st.slack_error}</p>}

            {staff.length > 0 && (
              <>
                <table className="item-table">
                  <thead>
                    <tr>
                      <th>On the Square schedule</th>
                      <th>Slack account to DM</th>
                    </tr>
                  </thead>
                  <tbody>
                    {staff.map((s) => (
                      <tr key={s.team_member_id}>
                        <td>
                          {s.name}
                          <span className="muted">
                            {' '}
                            — {s.shifts} upcoming{' '}
                            {s.shifts === 1 ? 'shift' : 'shifts'}
                          </span>
                        </td>
                        <td>
                          <select
                            value={mappedFor(s.team_member_id)}
                            disabled={busy || slackUsers.length === 0}
                            onChange={(e) =>
                              setMapping(s.team_member_id, e.target.value)
                            }
                          >
                            <option value="">Nobody — no notices</option>
                            {slackUsers.map((u) => (
                              <option key={u.slack_user_id} value={u.slack_user_id}>
                                {u.name}
                              </option>
                            ))}
                          </select>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                {unmapped > 0 && (
                  <p className="field-note">
                    {unmapped} {unmapped === 1 ? 'person is' : 'people are'} on
                    the schedule with nobody selected. They&rsquo;ll work
                    without hearing what&rsquo;s on.
                  </p>
                )}
              </>
            )}
          </section>

          <section className="panel">
            <h2 className="panel-title">Today&rsquo;s notices</h2>
            <p className="field-note">
              These messages carry private-booking details to named people, so
              check the preview before sending. Sending twice is safe — an
              unchanged notice already delivered isn&rsquo;t repeated.
            </p>
            <button className="btn" onClick={preview} disabled={busy}>
              {busy ? 'Working…' : 'Preview'}
            </button>{' '}
            <button className="btn" onClick={send} disabled={busy}>
              Send now
            </button>
            {plans !== null && <NoticePreview plans={plans} />}
            <NoticeHistory runs={recent} />
          </section>
        </>
      )}
    </div>
  );
}

function NoticePreview({ plans }: { plans: EventsNoticePlan[] }) {
  if (plans.length === 0) {
    return (
      <p className="muted">
        Nobody would be notified: either nobody is on the schedule today,
        nothing is on, or the people working aren&rsquo;t mapped yet.
      </p>
    );
  }
  return (
    <div>
      {plans.map((p) => (
        <div key={p.slack_user_id}>
          <h3 className="panel-title">To {p.name}</h3>
          <pre className="preview-body">{p.body}</pre>
        </div>
      ))}
    </div>
  );
}

function NoticeHistory({ runs }: { runs: EventsNoticeRun[] }) {
  if (runs.length === 0) {
    return <p className="muted">No notices have gone out yet.</p>;
  }
  return (
    <table className="item-table">
      <thead>
        <tr>
          <th>When</th>
          <th>Result</th>
          <th>Trigger</th>
        </tr>
      </thead>
      <tbody>
        {runs.map((r, i) => (
          <tr key={i}>
            <td>{r.at}</td>
            <td>
              {r.ok ? (
                <>
                  <span className="pill pill-ok">OK</span>{' '}
                  {r.sent} sent
                  {r.unmapped > 0 ? `, ${r.unmapped} unmapped` : ''}
                </>
              ) : (
                <>
                  <span className="pill pill-off">Failed</span> {r.error}
                </>
              )}
            </td>
            <td>{r.triggered_by}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
