import type { ReactNode } from 'react';
import { frogSVG } from './art/frog.js';
import { HOP } from './consts.js';
import type { Frame, WireHeat, WirePlayer, WireResult } from './api';

// The main screen's words: one big card (the hero) saying what is happening
// to YOU now, plus the standings under it once there are any. Ported from the
// standalone Hoppers phone; the rules of what it says are unchanged.

export const nth = (n: number) =>
  n + (['th', 'st', 'nd', 'rd'][((n % 100) - 20) % 10] || ['th', 'st', 'nd', 'rd'][n % 100] || 'th');
export const pts = (n: number) => `${n} ${n === 1 ? 'pt' : 'pts'}`;

export function Frog({ p, mood }: { p: Pick<WirePlayer, 'color' | 'hat'> & { wins?: number; bot?: boolean }; mood?: 'wow' }) {
  const badge = !p.bot && (p.wins ?? 0) > 0;
  return <span className="frogart" dangerouslySetInnerHTML={{ __html: frogSVG(p.color, p.hat, { mood, badge }) }} />;
}

// Hero is the one big card. It pops when what it says changes, not on every
// frame: the key remounts it, which replays the CSS animation.
export interface HeroSpec {
  cls: string;
  kicker: string;
  big: ReactNode;
  sub?: ReactNode;
  key: string;
}

export function Hero({ spec }: { spec: HeroSpec }) {
  return (
    <div key={spec.key} className={`hero pop ${spec.cls}`}>
      {spec.kicker && <div className="k">{spec.kicker}</div>}
      {spec.big}
      {spec.sub && <div className="sub">{spec.sub}</div>}
    </div>
  );
}

const big = (text: string) => <div className="big">{text}</div>;
const place = (n: number, field?: number) => (
  <div className="place">
    {nth(n)}
    {field ? <small> of {field}</small> : null}
  </div>
);

const player = (s: Frame, id: string) => s.players.find((p) => p.id === id);
const multi = (s: Frame) => (s.heats ?? []).length > 1;
// "Race 2 of 3", or with heats "Round 2 of 3 · Heat 1" / "Round 3 of 3 · The Final"
const raceLabel = (s: Frame, h: WireHeat | undefined) =>
  h && multi(s) ? `Round ${s.round} of ${s.rounds} · ${h.name}` : `Race ${s.round} of ${s.rounds}`;

export function myHeat(s: Frame, id: string): WireHeat | undefined {
  const p = player(s, id);
  return p && p.heat ? (s.heats ?? [])[p.heat - 1] : undefined;
}
export const myStanding = (s: Frame, id: string) => (s.standings ?? []).find((x) => x.player === id);
const myResult = (h: WireHeat | undefined, id: string) => (h?.results ?? []).find((r) => r.player === id);

// heroFor is everything the big card says, for every screen but the maker.
export function heroFor(s: Frame, id: string): { hero: HeroSpec; result?: WireResult; standings: boolean } {
  if (s.phase === HOP.phase.lobby) {
    const n = s.players.filter((p) => !p.bot && p.connected).length;
    return { hero: { cls: '', kicker: "You're in", big: big('Watch the TV'), sub: `${n} playing`, key: 'lobby' }, standings: false };
  }
  if (s.phase === HOP.phase.final) return { hero: overHero(s, id), standings: true };
  return raceHero(s, id);
}

function overHero(s: Frame, id: string): HeroSpec {
  const score = player(s, id)?.score ?? 0;
  if (s.champion === id) return { cls: 'gold', kicker: 'Game over', big: big('You win'), sub: pts(score), key: 'won' };
  const st = myStanding(s, id);
  const champ = player(s, s.champion);
  return {
    cls: '', kicker: 'Game over',
    big: st ? place(st.rank) : big('Thanks!'),
    sub: `${pts(score)}${champ ? ` · ${champ.name} wins` : ''}`,
    key: 'over',
  };
}

function raceHero(s: Frame, id: string): { hero: HeroSpec; result?: WireResult; standings: boolean } {
  const heats = s.heats ?? [];
  const cur = heats[s.heat - 1];
  const h = myHeat(s, id);
  if (!h) {
    return { hero: { cls: '', kicker: raceLabel(s, cur), big: big('Watching'), sub: "You'll race in the next race.", key: 'watching' }, standings: false };
  }
  const res = myResult(h, id);
  // Result: my race is over (the rest of the night may still be racing)
  if (res && (h.status === HOP.heat.done || (h === cur && s.sub === HOP.sub.raceEnd))) {
    const next = s.roundOver ? (s.lastRace ? 'Final scores next.' : 'Next race soon.')
      : h === cur ? 'Next race soon.' : `Now racing: ${cur ? cur.name : ''}.`;
    const st = myStanding(s, id);
    const total = player(s, id)?.score ?? 0;
    const sub = (
      <>
        <span className={`gain ${res.points ? '' : 'zero'}`}>+{pts(res.points)}</span>
        <br />
        {pts(total)} tonight{st ? ` · ${nth(st.rank)}` : ''}. {next}
      </>
    );
    return { hero: { cls: res.place === 1 ? 'gold' : '', kicker: raceLabel(s, h), big: place(res.place), sub, key: `res-${s.round}-${h.num}` }, result: res, standings: true };
  }
  if (h !== cur) {
    // Wait: my heat hasn't started
    if (h.status === HOP.heat.next) {
      return { hero: { cls: 'next', kicker: raceLabel(s, h), big: big('Your heat is next'), sub: h.final ? 'The Final.' : 'Get your phone out.', key: `next-${h.num}` }, standings: false };
    }
    return { hero: { cls: '', kicker: raceLabel(s, h), big: big('Waiting'), sub: `${cur ? cur.name : 'A heat'} is racing. Yours is ${h.name}.`, key: `wait-${h.num}` }, standings: false };
  }
  // Wait: my race is on. One number: my place in this race.
  const ranking = s.ranking ?? [];
  const wrong = (s.backward ?? []).includes(id);
  const deck = s.sub === HOP.sub.flick && s.onDeck === id;
  const hopper = player(s, s.current);
  let line: ReactNode = '';
  if (s.sub === HOP.sub.ready) line = 'Get ready.';
  else if (deck) line = <b>You're up next.</b>;
  else if (s.sub === HOP.sub.flick && hopper) line = `${hopper.name} is hopping.`;
  const sub = (
    <>
      {wrong && <div className="wrongway">⬅ Your frog is going the wrong way</div>}
      {line}
    </>
  );
  const cls = deck ? 'next' : wrong ? 'wrong' : 'go';
  const mine = ranking.indexOf(id) + 1;
  return {
    hero: { cls, kicker: raceLabel(s, h), big: place(mine, ranking.length), sub, key: `race-${cls}-${mine}-${ranking.length}` },
    standings: false,
  };
}

// Standings: the top ten, plus me if I'm further down.
export function Standings({ s, id }: { s: Frame; id: string }) {
  const list = s.standings ?? [];
  if (!list.length) return null;
  const mi = list.findIndex((x) => x.player === id);
  const rows = list.filter((_, i) => i < 10 || i === mi);
  return (
    <div className="card">
      <h3>
        Standings <span>Every place scores</span>
      </h3>
      <div className="rows">
        {rows.map((x) => {
          const p = player(s, x.player);
          if (!p) return null;
          return (
            <div key={x.player} className={`lrow ${x.player === id ? 'mine' : ''}`}>
              <b>{x.player === s.champion ? '👑' : x.rank}</b>
              <Frog p={p} />
              <span className="nm">{p.name}</span>
              <span className="pt">{x.points}</span>
            </div>
          );
        })}
      </div>
    </div>
  );
}

export function HowToPlay() {
  return (
    <div className="how">
      Flick your frog up when it's your turn. Hit the green on the meter for a bonus hop. First to the finish wins.
      <br />
      Land on a frog and you ride it. Frogs sometimes turn around.
    </div>
  );
}
