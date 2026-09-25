import { ACTION, boardIsEmpty, extraAction, money, PHASE, primaryAction, roundLabel, type HostFrame, type HostTeam } from './common';
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
  const extra = extraAction(frame.phase, boardIsEmpty(frame), frame.roundCount);

  return (
    <aside className="trivia-panel">
      <p className="trivia-say">{cueFor(frame)}</p>

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

      <AwardList frame={frame} />

      <WaitingOn frame={frame} />

      <div className="page-head-actions">
        {primary ? (
          <button className="btn" disabled={busy} onClick={() => onAct({ action: primary.action })}>
            {primary.label}
          </button>
        ) : null}
        {/* Ghost, not primary: "another round?" is the room's decision and
            the host relays it, so it must not sit in the same place and the
            same weight as the button they press without looking. */}
        {extra ? (
          <button className="btn btn-ghost btn-spaced" disabled={busy}
            onClick={() => onAct({ action: extra.action })}>
            {extra.label}
          </button>
        ) : null}
        {secs !== null ? (
          <button className="btn btn-danger" disabled={busy}
            onClick={() => onAct({ action: ACTION.EXTEND, seconds: 15 })}>
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

// The honorable mentions, in the order the wall is about to show them.
//
// The host is meant to read these out, which is the entire reason they are
// here: without them the host is reading the TV over their own shoulder,
// backwards, in the dark.
function AwardList({ frame }: { frame: HostFrame }) {
  if (!frame.awards?.length) return null;
  return (
    <div className="trivia-awards">
      <p className="card-desc">Honorable mentions, in this order</p>
      <ol>
        {frame.awards.map((a) => (
          <li key={a.key}>
            <strong>{a.title}</strong>: {a.teamName}. {a.detail}
          </li>
        ))}
      </ol>
    </div>
  );
}

export function waitingTeams(frame: HostFrame): HostTeam[] {
  // A table that joined mid-round is not in this round's denominator, and it
  // is not something the host is waiting for either.
  const live = frame.teams.filter((t) => t.eligible);
  if (frame.phase === PHASE.WAGER) return live.filter((t) => !t.stakeLocked);
  if (frame.phase === PHASE.QUESTION) return live.filter((t) => !t.answered);
  // chipsPerTable, not tokens.length: a final deals one chip while tokens
  // still lists two, so this waited forever on a room that had all bet.
  if (frame.phase === PHASE.BETTING) return live.filter((t) => t.chipsPlaced < frame.chipsPerTable);
  return [];
}

function waitingLabel(frame: HostFrame): string {
  if (frame.phase === PHASE.BETTING) return 'Still placing chips';
  if (frame.phase === PHASE.WAGER) return 'No wager yet';
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
  if (frame.phase === PHASE.WAGER) return t.stakeLocked ? ' 🔒 locked' : ' waiting';
  if (frame.phase === PHASE.QUESTION) return t.answered ? ' in' : ' waiting';
  if (frame.phase === PHASE.BETTING) return ` ${t.chipsPlaced}/${frame.chipsPerTable}`;
  const d = frame.scoring?.deltas?.[t.id] ?? frame.lastRound?.deltas?.[t.id];
  return d === undefined || d === 0 ? '' : ` ${signed(d)}`;
}

// The line the host reads out. One sentence, per phase, saying what is
// happening and what to say about it.
export function cueFor(frame: HostFrame): string {
  switch (frame.phase) {
    case PHASE.SETUP:
    case PHASE.LOBBY: return cueLobby(frame);
    case PHASE.BOARD: return cueBoard(frame);
    case PHASE.INTERMISSION: return cueIntermission(frame);
    case PHASE.WAGER: return cueWager(frame);
    case PHASE.QUESTION: return cueQuestion(frame);
    case PHASE.REVEAL: return 'Cards are up. Betting opens next.';
    case PHASE.BETTING: return cueBetting(frame);
    case PHASE.SCORING: return cueScoring(frame);
    case PHASE.AWARDS: return cueAwards(frame);
    case PHASE.PODIUM: return cuePodium(frame);
  }
}

// The mentions are up and nothing is on a clock. The cue tells the host what
// to say and reminds them the list is right below it.
function cueAwards(frame: HostFrame): string {
  const n = frame.awards.length;
  if (!n) return 'Honorable mentions are up. Show the winner when ready.';
  return `Honorable mentions are up. Read the ${n} below out, then show the winner.`;
}

function cueLobby(frame: HostFrame): string {
  const n = frame.teams.length;
  if (!n) return 'No tables yet. The join code is on the screen.';
  return `${n} ${plural(n, 'table')} in. Start when ready.`;
}

// The break. The host is holding the room, not a clock, so the cue tells them
// what to say and what is coming rather than counting anything down.
function cueIntermission(frame: HostFrame): string {
  // Already one-based on the wire, and already pointing at the round that is
  // about to start: the break is entered by crossing into it.
  const leader = [...frame.teams].sort((a, b) => b.score - a.score)[0];
  const lead = leader ? ` ${leader.name} leads on ${money(leader.score)}.` : '';
  // The frame already carries the NEXT round's cells and chips — the break is
  // entered by crossing into it — so the cue can name the real numbers rather
  // than assert a multiple.
  // The break before the final has no board behind it, so it names the round
  // rather than money nobody is about to play for.
  if (boardIsEmpty(frame) && frame.finalWager && !frame.progress.finalPlayed) {
    return `Break. The final is next. They set their wagers before they see the question.${lead}`;
  }
  const cell = frame.board[0] ? `Every square is ${money(frame.board[0].points)}. ` : '';
  const chips = frame.tokens.map((t) => money(t)).join(' and ');
  return `Break. ${roundLabel(frame)} is next. ${cell}Chips are ${chips}.${lead}`;
}

function cueBoard(frame: HostFrame): string {
  const boardEmpty = boardIsEmpty(frame);
  if (boardEmpty && frame.finalWager && !frame.progress.finalPlayed) {
    return 'The board is empty. The final is next.';
  }
  if (boardEmpty) return 'The board is empty. Take them to the podium.';
  return pickLine(frame) ?? 'Pick a cell to ask the first question';
}

// The wager line names the CATEGORY, because that is the only thing the host
// has to read out at this point — and the only thing they are allowed to.
function cueWager(frame: HostFrame): string {
  const r = frame.round;
  if (!r) return 'The final is opening';
  const locked = frame.teams.filter((t) => t.eligible && t.stakeLocked).length;
  const cat = r.category ? `${r.category}. ` : '';
  if (r.eligible > 0 && locked >= r.eligible) return `${cat}All ${r.eligible} wagers are locked. Ask the question.`;
  return `${cat}${locked} of ${r.eligible} wagers locked.`;
}

// THE CLOCK IS NOT IN THE SENTENCE. It has its own slot on this panel, big,
// directly under the cue, so repeating it here printed the same number twice
// and cost the host's spoken line its ending. What a host says out loud is
// "nine of twelve tables in"; the seconds are something they glance at.
//
// Every cue used to be <state> — <instruction>, seven of them, heard forty
// times in ninety minutes. The dash stays where it is a real interruption and
// goes where it was a comma wearing a hat.
function cueQuestion(frame: HostFrame): string {
  const r = frame.round;
  if (!r) return 'A question is up';
  if (r.eligible > 0 && r.answered >= r.eligible) return `All ${r.eligible} tables are in. Reveal the cards.`;
  return `${r.answered} of ${r.eligible} tables in.`;
}

function cueBetting(frame: HostFrame): string {
  const n = waitingTeams(frame).length;
  if (!n) return 'All chips are down. Score the round.';
  return `${n} ${plural(n, 'table')} still placing.`;
}

function cueScoring(frame: HostFrame): string {
  const sc = frame.scoring;
  if (!sc) return 'Round scored';
  const card = frame.slots.find((s) => s.id === sc.winningSlot);
  const answer = `Answer: ${sc.correctText || sc.correctValue}.`;
  const won = card
    ? ` Winning card ${card.label}${card.teams.length ? ` (${card.teams.join(' and ')})` : '. Nobody wrote it'}.`
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
  if (!top) return "That's the game";
  // A tie is a tie: two tables on the same money both won, and the phone
  // already tells each of them so.
  const winners = frame.teams.filter((t) => t.score === top.score).map((t) => t.name);
  if (winners.length > 1) return `Winners: ${winners.join(' and ')} with ${money(top.score)}`;
  return `Winner: ${top.name} with ${money(top.score)}`;
}

function plural(n: number, word: string): string {
  return n === 1 ? word : `${word}s`;
}
