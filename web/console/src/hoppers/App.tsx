import { useEffect, useRef, useState } from 'react';
import { useLiveStream } from '../live/liveStream';
import { useFrameBuildReload } from '../live/useFrameBuildReload';
import { useWakeLock } from '../live/useWakeLock';
import { base, flick, join, reclaim, sendFeedback, type Frame, type FrogDraft } from './api';
import { RateNight } from '../live/RateNight';
import { Frog, Hero, HowToPlay, Standings, heroFor } from './screens';
import { Flick, FLICK_HOLD_MS, myTurn } from './Flick';
import { Maker, newDraft, saveDraft } from './Maker';
import { useMoments } from './moments';
import { buzz } from './buzz';
import { HOP } from './consts.js';

// The Hoppers phone. Four screens: Join (the frog maker), Wait, FLICK!, and
// Result. Who this phone is comes from the frame's `you`, which the server
// fills from the HttpOnly cookie; nothing here ever holds the token.
export default function App() {
  const { frame, connected, msLeft, apply, reopen } = useLiveStream<Frame>({
    streamUrl: base + '/stream',
    stateUrl: base + '/state',
  });
  const [making, setMaking] = useState<FrogDraft | null>(null);
  const [busy, setBusy] = useState(false);
  const [toast, setToast] = useState<{ msg: string; n: number } | null>(null);
  const [flickedTurn, setFlickedTurn] = useState('');
  const [away, setAway] = useState(false);
  // Set from the join or reclaim response until a frame arrives that says so:
  // frames still in flight from before the new cookie are a spectator's, and
  // must not throw a phone that has just joined back to the maker.
  const expect = useRef('');

  const id = frame?.you?.id ?? '';
  const mine = frame?.players.find((p) => p.id === id);
  const turnKey = frame ? `${frame.round}-${frame.heat}-${frame.turn}` : '';
  const flicking = (myTurn(frame, id) && flickedTurn !== turnKey) || away;

  useWakeLock();
  // Never reload under a thumb: not mid-flick, not while making a frog.
  const holding = useRef(false);
  holding.current = !!making || flicking;
  useFrameBuildReload(frame, { hold: () => holding.current });
  const flickHideAt = useMoments(frame, id);

  useEffect(() => {
    if (!frame) return;
    if (frame.you) {
      if (frame.you.id === expect.current) expect.current = '';
      return;
    }
    // A spectator: never joined, or this phone's frog moved to another phone.
    if (!expect.current && !making) setMaking(newDraft(frame));
  }, [frame, making]);

  const say = (msg: string) => setToast((t) => ({ msg, n: (t?.n ?? 0) + 1 }));
  useEffect(() => {
    if (!toast) return;
    const t = window.setTimeout(() => setToast(null), 2500);
    return () => window.clearTimeout(t);
  }, [toast]);

  const onDone = async (f: FrogDraft) => {
    saveDraft(f);
    setBusy(true);
    const res = await join(f);
    setBusy(false);
    if (!res.ok || !res.data) {
      say(res.error || "Couldn't join, try again");
      return;
    }
    setMaking(null);
    buzz(40);
    if (res.data.playerId !== id) {
      // EventSource sends the cookies it had when it connected.
      expect.current = res.data.playerId;
      reopen();
    }
  };

  const onReclaim = async (playerId: string, code: string) => {
    const res = await reclaim(playerId, code);
    if (!res.ok) {
      say("That code didn't work");
      return;
    }
    expect.current = playerId;
    setMaking(null);
    buzz([30, 40, 90]);
    reopen();
  };

  // This phone's last flick, shown as a faint tick on the next turn's meter.
  const [lastPower, setLastPower] = useState(-1);
  const onFlick = async (power: number, perfect: boolean, swiped: boolean) => {
    setFlickedTurn(turnKey);
    setAway(true);
    buzz(perfect ? [40, 30, 40, 30, 120] : 40);
    if (swiped) setLastPower(power);
    flickHideAt.current = Date.now() + FLICK_HOLD_MS;
    window.setTimeout(() => setAway(false), FLICK_HOLD_MS);
    const res = await flick(power, perfect);
    if (res.ok && res.data) apply(res.data);
  };


  return (
    <>
      {!connected && <NetPill />}
      {making ? (
        <Maker
          key={id || 'new'} frame={frame} me={id} initial={making} busy={busy}
          onDone={(f) => void onDone(f)} onReclaim={(p, c) => void onReclaim(p, c)}
        />
      ) : (
        <Main frame={frame} id={id} onEdit={() => mine && setMaking({ name: mine.name, color: mine.color, hat: mine.hat })} />
      )}
      {frame && flicking && mine && (
        <Flick
          // Held on the turn that flicked while its meter is up: the server
          // moves the turn on at once, and a new key would wipe the meter.
          key={away ? flickedTurn : turnKey} frame={frame} color={mine.color} hat={mine.hat} msLeft={msLeft} lastPower={lastPower}
          onFlick={(p, x, sw) => void onFlick(p, x, sw)} />
      )}
      {toast && <div id="toast" key={toast.n} className="show">{toast.msg}</div>}
    </>
  );
}

function Main({ frame, id, onEdit }: { frame: Frame | null; id: string; onEdit: () => void }) {
  if (!frame || !id) {
    return (
      <div id="main">
        <div className="hero"><div className="sub">Connecting…</div></div>
      </div>
    );
  }
  const mine = frame.players.find((p) => p.id === id);
  const { hero, standings } = heroFor(frame, id);
  // No editing mid-race: the frog on the TV must stay the one you picked.
  const racingNow = frame.phase === HOP.phase.racing && !!mine?.heat
    && (frame.heats ?? [])[mine.heat - 1]?.status !== HOP.heat.done;
  return (
    <div id="main">
      {mine && (
        <div className="me">
          <div className="face" id="meFace"><Frog p={mine} /></div>
          <div className="name">{mine.name}</div>
        </div>
      )}
      <Hero spec={hero} />
      {frame.phase === HOP.phase.lobby && <HowToPlay />}
      {standings && <Standings s={frame} id={id} />}
      {frame.phase === HOP.phase.final && (
        <RateNight game={frame.game} storageKey="kit.hoppers.feedback" send={sendFeedback} />
      )}
      {!racingNow && <button className="btn ghost" id="edit" onClick={onEdit}>Edit frog</button>}
    </div>
  );
}

// Only show the pill for an outage, not a blip.
function NetPill() {
  const [show, setShow] = useState(false);
  useEffect(() => {
    const t = window.setTimeout(() => setShow(true), 3000);
    return () => window.clearTimeout(t);
  }, []);
  return show ? <div id="net">Reconnecting…</div> : null;
}
