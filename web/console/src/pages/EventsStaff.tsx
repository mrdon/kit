import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  api,
  type EventsChannelOption,
  type EventsDayNotice,
  type EventsNoticeRun,
  type EventsStaff,
} from '../api';
import { useSetChatContext } from '../chatContext';

// Admin page for shift notices: pick the channel the daily post goes to, and
// pair each person on the Square schedule with their Slack account so the post
// can @-mention them.
//
// Every id involved is opaque — Square's TM…, Slack's U… and C… — so this page
// never shows one. Name-labelled dropdowns, ids kept behind them.
//
// The mapping is optional by design. Unmapped staff are still named in the
// post; they just are not pinged. So notices work the day the channel is set,
// and mapping is an improvement rather than a precondition.

export default function EventsStaffPage() {
  useSetChatContext('the admin Events staff notices page');
  const [st, setSt] = useState<EventsStaff | null>(null);
  const [notice, setNotice] = useState<EventsDayNotice | null | undefined>(
    undefined,
  );
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
      // A changed mapping changes who the post would mention, so a preview
      // rendered against the old pairing is worse than none.
      setNotice(undefined);
      setNote(slackUserID ? 'Mapping saved.' : 'Mapping cleared.');
    });

  const setChannel = (channelID: string) =>
    run(async () => {
      setSt(await api.saveEventsNoticeChannel({ channel_id: channelID }));
      setNotice(undefined);
      setNote(channelID ? 'Channel saved.' : 'Notices turned off.');
    });

  const preview = () =>
    run(async () => {
      const r = await api.previewEventsNotices();
      setNotice(r.notice);
    });

  const send = () =>
    run(async () => {
      const r = await api.sendEventsNotices();
      setSt(r.staff);
      setNotice(undefined);
      setNote(r.message);
    });

  const staff = st?.staff ?? [];
  const slackUsers = st?.slack_users ?? [];
  const mappings = st?.mappings ?? [];
  const channels = st?.channels ?? [];
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
          Each morning at 8am, Kit posts the day to a channel — who&rsquo;s
          working and what&rsquo;s on, private bookings included — mentioning
          the people on shift, with the per-event detail in a thread.
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
            <h2 className="panel-title">Where notices go</h2>
            {st.channels_error && <p className="muted">{st.channels_error}</p>}
            <label className="field">
              <span>Channel</span>
              <select
                value={st.notice_channel_id}
                disabled={busy || channels.length === 0}
                onChange={(e) => setChannel(e.target.value)}
              >
                <option value="">Nowhere — notices are off</option>
                {channels.map((c: EventsChannelOption) => (
                  <option key={c.id} value={c.id} disabled={!c.bot_is_member}>
                    {c.is_private ? '🔒 ' : '#'}
                    {c.name}
                    {c.bot_is_member ? '' : ' — invite Kit first'}
                  </option>
                ))}
              </select>
              <span className="field-note">
                Kit has to be in the channel to post. If the one you want is
                greyed out, run <code>/invite @Kit</code> there and reload.
              </span>
            </label>
          </section>

          <section className="panel">
            <h2 className="panel-title">Who gets mentioned</h2>
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
                            <option value="">Not mentioned</option>
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
                    the schedule without a Slack account selected. They&rsquo;re
                    still named in the post — they just won&rsquo;t be notified.
                  </p>
                )}
              </>
            )}
          </section>

          <section className="panel">
            <h2 className="panel-title">Today&rsquo;s notices</h2>
            <p className="field-note">
              The post carries private-booking detail into a room, so read the
              preview before it goes out. Posting twice is safe — an unchanged
              notice already posted isn&rsquo;t repeated.
            </p>
            <button className="btn" onClick={preview} disabled={busy}>
              {busy ? 'Working…' : 'Preview'}
            </button>{' '}
            <button className="btn" onClick={send} disabled={busy}>
              Send now
            </button>
            {notice !== undefined && <NoticePreview notice={notice} />}
            <NoticeHistory runs={recent} />
          </section>
        </>
      )}
    </div>
  );
}

function NoticePreview({ notice }: { notice: EventsDayNotice | null }) {
  if (!notice) {
    return <p className="muted">Nothing would be posted: nothing is on today.</p>;
  }
  return (
    <div>
      <h3 className="panel-title">Posted to the channel</h3>
      <pre className="preview-body">{notice.headline}</pre>
      {notice.detail && (
        <>
          <h3 className="panel-title">In a thread under it</h3>
          <pre className="preview-body">{notice.detail}</pre>
        </>
      )}
      {notice.unmapped > 0 && (
        <p className="field-note">
          {notice.unmapped} working without a Slack account selected — named in
          the post, but not notified.
        </p>
      )}
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
                  {r.posted
                    ? `posted, ${r.mentions} mentioned`
                    : r.skipped
                      ? 'already current'
                      : 'nothing on'}
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
