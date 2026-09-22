import { boardIsEmpty, money, primaryAction, type HostFrame, type HostTeam } from './common';
import { pickLine, signed } from './live_lastround';
import { TeamBoard } from './live_tables';

// The status board half of the live page.
//
// A host stands at the bar with this on a laptop and has to CALL THINGS OUT
// over a room. So the top line of the panel is a sentence they can read
// aloud, not a phase name — what is happening and what to say about it — and
// everything under it answers the one question that line raises: which
// tables am I waiting on, what is the answer, who won.
//
// The one big primary button stays exactly where it was, because a host
// learns its position and stops reading the screen.

export function StatusPanel({ frame, busy, secs, gameId, skipReveal, onAct }: {
  frame: HostFrame;
  busy: boolean;
  secs: number | null;
  gameId: string;
  // With no deal beat the same click opens betting, and the button says so.
  skipReveal: boolean;
  onAct: (body: Record<string, unknown>) => void;
}) {
  const primary = primaryAction(frame.phase, boardIsEmpty(frame), frame.finalWager, frame.progress.finalPlayed, skipReveal);

  return (
    <aside className="trivia-panel">
      <p className="trivia-say">{cueFor(frame, secs)}</p>

      {/* The host sees the correct answer in every phase. They are reading it
          out and adjudicating nothing, so hiding it would be theatre with a
          cost. */}
      {frame.round ? (
        <>
          <p className="card-desc">
            {frame.round.isFinal ? 'FINAL · ' : ''}Question {frame.round.ordinal} · {money(frame.round.points)}
            {frame.round.category ? ` · ${frame.round.category}` : ''}
          </p>
          {/* Blank during the wager, and that is the point: the prompt is not
              on this frame either, so the host cannot read it out before the
              room has put its money up. */}
          <p className="trivia-question">{frame.round.text}</p>
          {frame.answer ? (
            <p className="trivia-answer">Answer: <strong>{frame.answer.text || frame.answer.value}</strong></p>
          ) : null}
        </>
      ) : null}

      {secs !== null ? <div className="trivia-clock">{secs}</div> : null}

      <WaitingOn frame={frame} />

      <div className="page-head-actions">
        {primary ? (
          <button className="btn" disabled={busy} onClick={() => onAct({ action: primary.action })}>
            {primary.label}
          </button>
        ) : null}
        {secs !== null ? (
          <button className="btn btn-danger" disabled={busy}
            onClick={() => onAct({ action: 'extend', seconds: 15 })}>
            +15s
          </button>
        ) : null}
      </div>

      {/* Rank, name, movement, phase state and score — one list, directly
          under the button, where the standings section used to be a screen
          and a half away. */}
      <TeamBoard frame={frame} gameId={gameId} />
    </aside>
  );
}

// Who is holding the room up. A count tells the host to wait; a list of names
// tells them which table to go and lean on, which is the whole difference.
function WaitingOn({ frame }: { frame: HostFrame }) {
  const waiting = waitingTeams(frame);
  if (!waiting.length) return null;
  return (
    <div className="trivia-wait">
      <span className="card-desc">{waitingLabel(frame)}</span>
      <div className="teamlist">
        {waiting.map((t) => <span key={t.id} className="pill pill-off">{t.name}</span>)}
      </div>
    </div>
  );
}

export function waitingTeams(frame: HostFrame): HostTeam[] {
  // A table that joined mid-round is not in this round's denominator, and it
  // is not something the host is waiting for either.
  const live = frame.teams.filter((t) => t.eligible);
  if (frame.phase === 'wager') return live.filter((t) => !t.stakeLocked);
  if (frame.phase === 'question') return live.filter((t) => !t.answered);
  if (frame.phase === 'betting') return live.filter((t) => t.chipsPlaced < frame.tokens.length);
  return [];
}

function waitingLabel(frame: HostFrame): string {
  if (frame.phase === 'betting') return 'Still placing chips';
  if (frame.phase === 'wager') return 'No wager yet';
  return 'Not answered yet';
}

// The pill's colour: green once this table has done whatever the phase is
// waiting on, grey while the host is still waiting for them.
export function teamPill(frame: HostFrame, t: HostTeam): string {
  const done = !waitingTeams(frame).some((w) => w.id === t.id);
  return done ? 'pill pill-ok' : 'pill pill-off';
}

// What this table has done in the phase the game is actually in. In the final
// that is locked-or-not and never the amount: the stake belongs to the phone
// that typed it until the round is scored.
export function teamState(frame: HostFrame, t: HostTeam): string {
  // A table that joined mid-question is not waiting on anything — it is
  // sitting this one out — so it gets the question it comes in on rather than
  // a status it cannot act on. The ordinal is derived here rather than read
  // off the team: `inFromQuestion` rides on the phone's private frame only,
  // and the host already has the round in play.
  if (!t.eligible && frame.round) return ` in from Q${frame.round.ordinal + 1}`;
  if (frame.phase === 'wager') return t.stakeLocked ? ' 🔒 locked' : ' waiting';
  if (frame.phase === 'question') return t.answered ? ' in' : ' waiting';
  if (frame.phase === 'betting') return ` ${t.chipsPlaced}/${frame.tokens.length}`;
  const d = frame.scoring?.deltas?.[t.id] ?? frame.lastRound?.deltas?.[t.id];
  return d === undefined || d === 0 ? '' : ` ${signed(d)}`;
}

// The line the host reads out. One sentence, per phase, saying what is
// happening and what to say about it.
export function cueFor(frame: HostFrame, secs: number | null): string {
  switch (frame.phase) {
    case 'setup':
    case 'lobby': return cueLobby(frame);
    case 'board': return cueBoard(frame);
    case 'intermission': return cueIntermission(frame);
    case 'wager': return cueWager(frame, secs);
    case 'question': return cueQuestion(frame, secs);
    case 'reveal': return `Cards are up — betting opens in ${secs ?? 0}s`;
    case 'betting': return cueBetting(frame, secs);
    case 'scoring': return cueScoring(frame);
    case 'podium': return cuePodium(frame);
  }
}

function cueLobby(frame: HostFrame): string {
  const n = frame.teams.length;
  if (!n) return 'No tables yet — the join code is on the screen';
  return `${n} ${plural(n, 'table')} in — start when ready`;
}

// The break. The host is holding the room, not a clock, so the cue tells them
// what to say and what is coming rather than counting anything down.
function cueIntermission(frame: HostFrame): string {
  const next = frame.boardRound + 1;
  const leader = [...frame.teams].sort((a, b) => b.score - a.score)[0];
  const lead = leader ? ` ${leader.name} leads on ${money(leader.score)}.` : '';
  return `Break — round ${next} of ${frame.boardRounds} is next, and everything in it is worth double.${lead}`;
}

function cueBoard(frame: HostFrame): string {
  const boardEmpty = boardIsEmpty(frame);
  if (boardEmpty && frame.finalWager && !frame.progress.finalPlayed) {
    return 'Board is empty — the final is next';
  }
  if (boardEmpty) return 'Board is empty — take them to the podium';
  return pickLine(frame) ?? 'Pick a cell to ask the first question';
}

// The wager line names the CATEGORY, because that is the only thing the host
// has to read out at this point — and the only thing they are allowed to.
function cueWager(frame: HostFrame, secs: number | null): string {
  const r = frame.round;
  if (!r) return 'The final is opening';
  const locked = frame.teams.filter((t) => t.eligible && t.stakeLocked).length;
  const cat = r.category ? `${r.category} — ` : '';
  if (r.eligible > 0 && locked >= r.eligible) return `${cat}all ${r.eligible} wagers locked — ask the question`;
  return `${cat}${locked} of ${r.eligible} wagers locked${secs === null ? '' : ` — ${secs}s`}`;
}

function cueQuestion(frame: HostFrame, secs: number | null): string {
  const r = frame.round;
  if (!r) return 'A question is in play';
  if (r.eligible > 0 && r.answered >= r.eligible) return `All ${r.eligible} in — reveal the cards`;
  return `${r.answered} of ${r.eligible} in${secs === null ? '' : ` — ${secs}s`}`;
}

function cueBetting(frame: HostFrame, secs: number | null): string {
  const n = waitingTeams(frame).length;
  if (!n) return 'All chips are down — score it';
  return `${n} ${plural(n, 'table')} still placing${secs === null ? '' : ` — ${secs}s`}`;
}

function cueScoring(frame: HostFrame): string {
  const sc = frame.scoring;
  if (!sc) return 'Round scored';
  const card = frame.slots.find((s) => s.id === sc.winningSlot);
  const answer = `Answer: ${sc.correctText || sc.correctValue}.`;
  const won = card
    ? ` Winning card ${card.label}${card.teams.length ? ` (${card.teams.join(' & ')})` : ' — nobody wrote it'}.`
    : ' Nobody wrote it.';
  return `${answer}${won}${movers(frame, sc.deltas)}`;
}

// The three biggest swings, in order. More than that is a list nobody can
// read aloud, and the rest are in the standings a few centimetres below.
function movers(frame: HostFrame, deltas: Record<string, number>): string {
  const top = frame.teams
    .map((t) => ({ name: t.name, d: deltas[t.id] ?? 0 }))
    .filter((r) => r.d !== 0)
    .sort((a, b) => Math.abs(b.d) - Math.abs(a.d))
    .slice(0, 3);
  if (!top.length) return ' Nobody moved.';
  return ` ${top.map((r) => `${r.name} ${signed(r.d)}`).join(', ')}.`;
}

function cuePodium(frame: HostFrame): string {
  const top = [...frame.teams].sort((a, b) => b.score - a.score)[0];
  if (!top) return 'That is the night';
  // A tie is a tie: two tables on the same money both won, and the phone
  // already tells each of them so.
  const winners = frame.teams.filter((t) => t.score === top.score).map((t) => t.name);
  if (winners.length > 1) return `Winners: ${winners.join(' and ')} with ${money(top.score)}`;
  return `Winner: ${top.name} with ${money(top.score)}`;
}

function plural(n: number, word: string): string {
  return n === 1 ? word : `${word}s`;
}
