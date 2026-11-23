import { useEffect, useState, type ReactNode } from 'react';
import type { EventsChannelOption, TriviaFeedbackChannel } from '../api';

// Where a live game's end-of-night ratings go: the admin panel shared by
// trivia and Hoppers. Each game stores its own channel; this is the picker.
// Unset until an admin picks one, and ratings sent before then go nowhere:
// they are not stored.
export function FeedbackChannelPanel({ load, save: saveChannel, children }: {
  load: () => Promise<TriviaFeedbackChannel>;
  save: (channelId: string) => Promise<TriviaFeedbackChannel>;
  // What gets rated, in a sentence or two, for the panel's intro.
  children: ReactNode;
}) {
  const [st, setSt] = useState<TriviaFeedbackChannel | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    load().then(setSt).catch((e) => setErr((e as Error).message));
  }, [load]);

  const save = async (channelId: string) => {
    setBusy(true);
    setErr(null);
    setNote(null);
    try {
      setSt(await saveChannel(channelId));
      setNote(channelId ? 'Channel saved.' : 'Ratings will not be sent.');
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const channels = st?.channels ?? [];
  return (
    <section className="panel">
      <h2>Where ratings go</h2>
      <p className="page-sub">{children}</p>
      {err ? <p className="banner banner-error">{err}</p> : null}
      {note ? <p className="banner banner-ok">{note}</p> : null}
      {st?.channels_error ? <p className="muted">{st.channels_error}</p> : null}
      {st ? (
        <label className="field">
          <span>Channel</span>
          <select value={st.channel_id} disabled={busy || channels.length === 0} onChange={(e) => void save(e.target.value)}>
            <option value="">Nowhere (ratings are not sent)</option>
            {channels.map((c: EventsChannelOption) => (
              <option key={c.id} value={c.id} disabled={!c.bot_is_member}>
                {c.is_private ? '🔒 ' : '#'}
                {c.name}
                {c.bot_is_member ? '' : ' (invite Kit first)'}
              </option>
            ))}
          </select>
          <span className="field-note">
            Kit has to be in the channel to post. If the one you want is greyed out,
            run <code>/invite @Kit</code> there and reload.
          </span>
        </label>
      ) : null}
    </section>
  );
}
