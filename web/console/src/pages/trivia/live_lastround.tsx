import { boardIsEmpty, money, type HostFrame, type HostLastRound, type HostTeam } from './common';

// The round that has already been put away.
//
// The host presses next, the game goes back to the board and the server
// clears the current round — and the very next thing out of the host's mouth
// is "Table X picks the next category". That sentence's data is here, and
// nowhere else on the page, which is why this block gets the full width and a
// heading rather than a line in the panel.

export function pickLine(last: HostLastRound): string {
  if (!last.winners.length) {
    return 'Nobody wrote the winning card — you pick the next category';
  }
  const verb = last.winners.length > 1 ? 'pick' : 'picks';
  return `${last.winners.join(' & ')} ${verb} the next category`;
}

export function LastRoundRecap({ frame }: { frame: HostFrame }) {
  const last = frame.lastRound;
  if (!last) return null;
  const answer = last.correctText || String(last.correctValue);
  // With the board emptied there is no category left to pick, so saying so
  // would send the host looking for a cell that is not there.
  const picks = frame.phase === 'board' && !boardIsEmpty(frame);
  return (
    <section className="panel trivia-recap">
      <h2>Last round · {last.isFinal ? 'the final' : `question ${last.ordinal}`}</h2>
      {picks ? <p className="trivia-say">{pickLine(last)}</p> : null}
      <p className="card-desc">{last.text}</p>
      <p className="trivia-answer">
        Answer was <strong>{answer}</strong>
        {last.winningLabel
          ? <> · winning card <strong>{last.winningLabel}</strong>
              {last.winners.length ? ` — ${last.winners.join(' & ')}` : ''}</>
          : <> · nobody wrote it</>}
      </p>
      <Movement teams={frame.teams} board={last.boardPoints} bets={last.betDeltas} />
    </section>
  );
}

// Both channels, separately: a table that took the cell and a table whose
// chip paid did different things, and the host calls them out differently.
export function Movement({ teams, board, bets }: {
  teams: HostTeam[];
  board: Record<string, number>;
  bets: Record<string, number>;
}) {
  const rows = teams
    .map((t) => ({ t, card: board[t.id] ?? 0, bet: bets[t.id] ?? 0 }))
    .filter((r) => r.card !== 0 || r.bet !== 0)
    .sort((a, b) => (b.card + b.bet) - (a.card + a.bet));

  if (!rows.length) return <p className="card-desc">Nobody moved that round.</p>;
  return (
    <div className="teamlist">
      {rows.map((r) => (
        <span key={r.t.id} className={r.card + r.bet >= 0 ? 'pill pill-ok' : 'pill pill-error'}>
          {r.t.name} {signed(r.card + r.bet)}
          <span className="trivia-split">
            {r.card ? ` ${signed(r.card)} card` : ''}{r.bet ? ` ${signed(r.bet)} bets` : ''}
          </span>
        </span>
      ))}
    </div>
  );
}

// The movement to show for "this round": the round being scored if one is,
// otherwise the one that was scored last. Between questions those are the
// same numbers — the host has just stopped being able to see them.
export function roundMovement(frame: HostFrame): { board: Record<string, number>; bets: Record<string, number> } | null {
  if (frame.scoring) return { board: frame.scoring.boardPoints, bets: frame.scoring.betDeltas };
  if (frame.lastRound) return { board: frame.lastRound.boardPoints, bets: frame.lastRound.betDeltas };
  return null;
}

export function signed(n: number): string {
  return (n > 0 ? '+' : '') + money(n);
}
