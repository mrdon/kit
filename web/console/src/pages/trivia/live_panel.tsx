import { useState } from 'react';
import { api } from '../../api';
import { boardIsEmpty, money, primaryAction, type HostFrame, type HostTeam } from './common';
import { pickLine, signed } from './live_lastround';

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

export function StatusPanel({ frame, busy, secs, gameId, onAct }: {
  frame: HostFrame;
  busy: boolean;
  secs: number | null;
  gameId: string;
  onAct: (body: Record<string, unknown>) => void;
}) {
  const primary = primaryAction(frame.phase, boardIsEmpty(frame), frame.finalWager, frame.progress.finalPlayed);

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
          </p>
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

      <TeamChips frame={frame} gameId={gameId} />
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
  if (frame.phase === 'question') {
    return frame.round?.isFinal
      ? live.filter((t) => !t.stakeLocked)
      : live.filter((t) => !t.answered);
  }
  if (frame.phase === 'betting') return live.filter((t) => t.chipsPlaced < frame.tokens.length);
  return [];
}

function waitingLabel(frame: HostFrame): string {
  if (frame.phase === 'betting') return 'Still placing chips';
  return frame.round?.isFinal ? 'No stake locked yet' : 'Not answered yet';
}

// One chip per table, lighting as answers, stakes and bets land — so the host
// can see WHICH table is holding everyone up rather than just a count.
function TeamChips({ frame, gameId }: { frame: HostFrame; gameId: string }) {
  const [code, setCode] = useState<{ team: string; code: string } | null>(null);
  const sorted = [...frame.teams].sort((a, b) => b.score - a.score);

  const reissue = async (teamId: string, name: string) => {
    try {
      const r = await api.triviaReclaim(gameId, teamId);
      setCode({ team: name, code: r.code });
    } catch {
      /* the host can just try again */
    }
  };

  return (
    <>
      <h3 className="card-title">Tables</h3>
      <div className="teamlist">
        {sorted.map((t) => (
          <button key={t.id} className={teamPill(frame, t)} title="Reissue this table's code"
            onClick={() => void reissue(t.id, t.name)}>
            {t.name} {money(t.score)}
            <span className="trivia-split">{teamState(frame, t)}</span>
          </button>
        ))}
      </div>
      {code ? (
        <p className="banner banner-ok">
          Read <strong>{code.code}</strong> to {code.team}. Their old phone is signed out.
        </p>
      ) : (
        <p className="card-desc">Tap a table to reissue its code if their phone died.</p>
      )}
    </>
  );
}

function teamPill(frame: HostFrame, t: HostTeam): string {
  const done = !waitingTeams(frame).some((w) => w.id === t.id);
  return done ? 'pill pill-ok' : 'pill pill-off';
}

// What this table has done in the phase the game is actually in. In the final
// that is locked-or-not and never the amount: the stake belongs to the phone
// that typed it until the round is scored.
function teamState(frame: HostFrame, t: HostTeam): string {
  if (frame.phase === 'question') {
    if (frame.round?.isFinal) return t.stakeLocked ? ' 🔒 locked' : ' waiting';
    return t.answered ? ' in' : ' waiting';
  }
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

function cueBoard(frame: HostFrame): string {
  const boardEmpty = boardIsEmpty(frame);
  if (boardEmpty && frame.finalWager && !frame.progress.finalPlayed) {
    return 'Board is empty — the final is next';
  }
  if (boardEmpty) return 'Board is empty — take them to the podium';
  return pickLine(frame) ?? 'Pick a cell to ask the first question';
}

function cueQuestion(frame: HostFrame, secs: number | null): string {
  const r = frame.round;
  if (!r) return 'A question is in play';
  const final = r.isFinal;
  const done = final ? frame.teams.filter((t) => t.eligible && t.stakeLocked).length : r.answered;
  const what = final ? 'stakes locked' : 'in';
  if (r.eligible > 0 && done >= r.eligible) return `All ${r.eligible} ${what} — reveal the cards`;
  return `${done} of ${r.eligible} ${what}${secs === null ? '' : ` — ${secs}s`}`;
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
  return `Winner: ${top.name} with ${money(top.score)}`;
}

function plural(n: number, word: string): string {
  return n === 1 ? word : `${word}s`;
}
