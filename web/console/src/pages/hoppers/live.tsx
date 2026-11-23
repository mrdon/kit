import { useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { api, API_BASE, type HoppersHostFrame } from '../../api';
import { useSetChatContext } from '../../chatContext';
import { useLiveStream } from '../../live/liveStream';
import { useFrameBuildReload } from '../../live/useFrameBuildReload';
import { SLUG } from '../../workspace';
import { frogSVG } from '../../hoppers/art/frog.js';
import { HOP } from '../../hoppers/consts.js';
import { PHASE_LABEL } from '../Hoppers';

// The live driver: the page a host runs the night from, on a laptop or on
// the taproom iPad. The TV has no input, so this is the only place the night
// moves on from: ONE big button, labelled by what the engine says it does
// right now (Start, Next heat, Next round, Final scores, New game).
//
// The host is a controller, not an authority: closing this page does not
// stop a race. Every press carries the stage it was made from, and a 409
// just means the game already moved.
export default function HoppersLive() {
  useSetChatContext('the Hoppers live page');
  const { id = '' } = useParams();
  const { frame, connected, msLeft, apply } = useLiveStream<HoppersHostFrame>({
    streamUrl: id ? `${API_BASE}/hoppers/games/${id}/stream` : null,
    stateUrl: id ? `${API_BASE}/hoppers/games/${id}/state` : null,
  });
  // A host is the one person who cannot afford to be a version behind.
  useFrameBuildReload(frame);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  if (!frame) return <p className="page-sub">{err ?? 'Connecting…'}</p>;

  const press = async () => {
    setBusy(true);
    setErr(null);
    try {
      apply(await api.hoppersAdvance(id, { phase: frame.phase, sub: frame.sub, round: frame.round, heat: frame.heat }));
    } catch (e) {
      const msg = (e as Error).message;
      setErr(msg.includes('409') || msg.includes('already happened') ? 'That already happened.' : msg);
    } finally {
      setBusy(false);
    }
  };

  const h = frame.host;
  const on = !!h?.canAdvance && !!h.advance;
  const tvURL = `${window.location.origin}/${SLUG}/hoppers/tv`;
  return (
    <div className="hop-live">
      <div className="page-head">
        <Link to="/hoppers" className="page-back">‹ Hoppers</Link>
        <h1>{PHASE_LABEL[frame.phase] ?? frame.phase}{frame.phase === HOP.phase.racing ? ` · ${raceLabel(frame)}` : ''}</h1>
        {!connected && <p className="pill pill-off">Reconnecting…</p>}
      </div>
      {err ? <p className="banner banner-error">{err}</p> : null}

      <button className="btn hop-advance" disabled={!on || busy} onClick={() => void press()}>
        {on ? h!.advance : status(frame, msLeft)}
      </button>
      {on && frame.phase === HOP.phase.final && (
        <p className="page-sub">New game clears the scores and starts again with the same frogs.</p>
      )}

      <Now frame={frame} msLeft={msLeft} />
      <Players frame={frame} gameId={id} />

      <p className="page-sub hop-links">
        TV: <code>{tvURL}</code>
      </p>
    </div>
  );
}

const playerName = (f: HoppersHostFrame, id: string) => f.players.find((p) => p.id === id)?.name ?? '';

function raceLabel(f: HoppersHostFrame) {
  if (f.final) return 'The Final';
  return (f.heats ?? []).length > 1 ? `Round ${f.round} · ${f.heatName}` : `Race ${f.round} of ${f.rounds}`;
}

// What the disabled button says while the game is running itself.
function status(f: HoppersHostFrame, msLeft: number | null) {
  if (f.phase === HOP.phase.lobby) return 'Waiting for frogs…';
  if (f.sub === HOP.sub.ready) return `Get ready${msLeft != null ? ` · ${Math.ceil(msLeft / 1000)}` : ''}`;
  return 'Racing…';
}

// Now is the one line about the race in progress: who is hopping, who is
// next, and how long they have.
function Now({ frame, msLeft }: { frame: HoppersHostFrame; msLeft: number | null }) {
  if (frame.phase !== HOP.phase.racing) return null;
  const last = frame.events?.length ? frame.events[frame.events.length - 1].text : '';
  return (
    <div className="card hop-now">
      {frame.sub === HOP.sub.flick && (
        <p>
          <b>{playerName(frame, frame.current)}</b> is up
          {msLeft != null ? ` · ${Math.ceil(msLeft / 1000)}s` : ''}
          {frame.onDeck ? ` · next ${playerName(frame, frame.onDeck)}` : ''}
        </p>
      )}
      {last && <p className="page-sub">{last}</p>}
    </div>
  );
}

// Players lists every phone that joined. "Code" is for a frog whose phone
// died: it signs the old phone out and gives a one-time code for the new one
// (the phone's "Already have a frog?" link). Read it out to them.
function Players({ frame, gameId }: { frame: HoppersHostFrame; gameId: string }) {
  const [codes, setCodes] = useState<Record<string, string>>({});
  const [err, setErr] = useState<string | null>(null);
  const people = frame.players.filter((p) => !p.bot);
  const points = new Map((frame.standings ?? []).map((s) => [s.player, s.points]));
  const issue = async (pid: string) => {
    setErr(null);
    try {
      const r = await api.hoppersReclaim(gameId, pid);
      setCodes((c) => ({ ...c, [pid]: r.code }));
    } catch (e) {
      setErr((e as Error).message);
    }
  };
  return (
    <section>
      <h2>Frogs ({people.length})</h2>
      {err ? <p className="banner banner-error">{err}</p> : null}
      {people.length === 0 && <p className="page-sub">Nobody yet. They scan the QR on the TV.</p>}
      <ul className="hop-players">
        {people
          .slice()
          .sort((a, b) => (points.get(b.id) ?? 0) - (points.get(a.id) ?? 0))
          .map((p) => (
            <li key={p.id} className={p.connected ? '' : 'is-away'}>
              <span className="hop-frog" dangerouslySetInnerHTML={{ __html: frogSVG(p.color, p.hat) }} />
              <span className="hop-name">{p.name}{p.connected ? '' : ' · away'}</span>
              <span className="hop-pts">{points.get(p.id) ?? 0}</span>
              {codes[p.id] ? (
                <span className="hop-code" title="Read this out. Their new phone enters it under 'Already have a frog?'">{codes[p.id]}</span>
              ) : (
                <button className="btn btn-ghost" onClick={() => void issue(p.id)}>Code</button>
              )}
            </li>
          ))}
      </ul>
    </section>
  );
}
