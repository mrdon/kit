import { useState } from 'react';
import { api } from '../../api';
import { money, PHASE, type HostFrame, type HostTeam } from './common';
import { roundMovement, signed } from './live_lastround';
import { teamPill, teamState } from './live_panel';

// The one list of tables.
//
// There used to be two. A wall of pills in the panel said who had answered;
// a "Standings" section below the board said who was winning — the same
// twenty names, in the same order, a screen and a half apart. The host had to
// read both to answer one question ("where is table four, and are they in
// yet?"), and with twenty tables the pills alone ran three screens deep.
//
// So: ONE ranked row per table, in the panel, under the button. Rank, name,
// what they did this round, the phase state the pills used to carry, and the
// score — one line each, in a box that scrolls on its own so the cue and the
// primary button never move.
//
// A row is still a button. "Our phone died" is the most common thing a host
// has to deal with all night, and tapping the table's name to reissue its
// code is the shortest path there is.

export function TeamBoard({ frame, gameId }: { frame: HostFrame; gameId: string }) {
  const [code, setCode] = useState<{ team: string; code: string } | null>(null);
  const sorted = [...frame.teams].sort((a, b) => b.score - a.score);
  const move = roundMovement(frame);

  const reissue = async (teamId: string, name: string) => {
    try {
      const r = await api.triviaReclaim(gameId, teamId);
      setCode({ team: name, code: r.code });
    } catch {
      /* the host can just try again */
    }
  };

  return (
    <div className="trivia-tables">
      <h3 className="card-title">Tables</h3>
      {sorted.length ? (
        <div className="trivia-rows">
          {sorted.map((t, i) => (
            <TeamRow key={t.id} frame={frame} team={t} rank={i + 1} move={move}
              onTap={() => void reissue(t.id, t.name)} />
          ))}
        </div>
      ) : (
        <p className="card-desc">Nobody has joined yet.</p>
      )}
      {code ? (
        <p className="banner banner-ok">
          Read <strong>{code.code}</strong> to {code.team}. Their old phone is signed out.
        </p>
      ) : (
        <p className="card-desc">Tap a table to reissue its code if their phone died.</p>
      )}
    </div>
  );
}

type Movement = { board: Record<string, number>; bets: Record<string, number> } | null;

function TeamRow({ frame, team, rank, move, onTap }: {
  frame: HostFrame;
  team: HostTeam;
  rank: number;
  move: Movement;
  onTap: () => void;
}) {
  const card = move?.board[team.id] ?? 0;
  const bet = move?.bets[team.id] ?? 0;
  const delta = card + bet;
  // A leader with nothing on the board is just whoever sorted first, and
  // crowning them in the lobby makes the mark mean nothing by question two.
  const leader = rank === 1 && team.score > 0;
  // Only on the board, and only while it is still their turn to choose: once
  // a cell is picked the tag is answering a question nobody is asking.
  const picksNext = frame.phase === PHASE.BOARD && frame.picker?.teamId === team.id;
  const state = teamState(frame, team).trim();
  // Outside question/betting teamState falls back to the round's delta, which
  // is the number the movement column is already showing. One of them has to
  // go, and the movement column says it in more detail.
  const chip = state && state !== signed(delta) ? state : '';

  return (
    <button type="button" className={leader ? 'trivia-row trivia-row-top' : 'trivia-row'}
      title={`Reissue ${team.name}'s code`} onClick={onTap}>
      {/* One line, and it does not wrap: the name gives up characters so that
          the score, the state and the tag all stay where the host's eye
          expects them down a list of twenty. */}
      <span className="trivia-line">
        <span className="trivia-rank">{leader ? '★' : rank}</span>
        <span className="trivia-name">{team.name}</span>
        {picksNext ? <span className="pill trivia-tag">picks next</span> : null}
        {delta ? (
          <span className={delta >= 0 ? 'trivia-move' : 'trivia-move trivia-move-down'}>
            {signed(delta)}
          </span>
        ) : null}
        {chip ? <span className={`${teamPill(frame, team)} trivia-tag`}>{chip}</span> : null}
        <span className="trivia-score">{money(team.score)}</span>
      </span>
      {/* Taking the cell and having a chip pay are different things the host
          calls out differently, so the two channels get named — but only for
          a table that actually did both, and on a line of its own. Inline,
          the breakdown is long enough to squeeze the table's NAME out of the
          row, which is the one thing on it nobody can do without. */}
      {card && bet ? (
        <span className="trivia-breakdown">
          {signed(card)} card · {signed(bet)} bets
        </span>
      ) : null}
    </button>
  );
}
