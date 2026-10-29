import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type EventsChannelOption, type TriviaFeedbackChannel } from '../api';
import { useSetChatContext } from '../chatContext';

// Where 86s are posted. Whoever finds the keg blown ticks it on the Menu page
// or the bar iPad, or tells Kit; this is the Slack channel that hears about
// it, so whoever reorders, and whoever is about to promise it to a table,
// knows. Unset until an admin picks one, and nothing is posted before then.
export default function MenuEightySixChannel() {
  useSetChatContext('the 86 alerts settings page');
  const [st, setSt] = useState<TriviaFeedbackChannel | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api.eightySixChannel().then(setSt).catch((e) => setErr((e as Error).message));
  }, []);

  const save = async (channelId: string) => {
    setBusy(true);
    setErr(null);
    setNote(null);
    try {
      setSt(await api.saveEightySixChannel(channelId));
      setNote(channelId ? 'Channel saved.' : '86s will not be posted.');
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const channels = st?.channels ?? [];
  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <Link to="/admin">Admin</Link>
          <span className="crumb-sep">/</span>
          <span>86 alerts</span>
        </nav>
        <h1>86 alerts</h1>
        <p className="page-sub">
          When someone 86s a beer, Kit posts it here, with who did it. Bringing
          a beer back is not posted.
        </p>
      </div>

      {err && <p className="banner banner-error">{err}</p>}
      {note && <p className="banner banner-ok">{note}</p>}
      {!st && !err && <p className="muted">Loading…</p>}

      {st && (
        <section className="panel">
          {st.channels_error && <p className="muted">{st.channels_error}</p>}
          <label className="field">
            <span>Channel</span>
            <select
              value={st.channel_id}
              disabled={busy || channels.length === 0}
              onChange={(e) => void save(e.target.value)}
            >
              <option value="">Nowhere (86s are not posted)</option>
              {channels.map((c: EventsChannelOption) => (
                <option key={c.id} value={c.id} disabled={!c.bot_is_member}>
                  {c.is_private ? '🔒 ' : '#'}
                  {c.name}
                  {c.bot_is_member ? '' : ' (invite Kit first)'}
                </option>
              ))}
            </select>
            <span className="field-note">
              Kit has to be in the channel to post. If the one you want is greyed
              out, run <code>/invite @Kit</code> there and reload.
            </span>
          </label>
        </section>
      )}
    </div>
  );
}
