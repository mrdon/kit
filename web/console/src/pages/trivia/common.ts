// Shared types and formatters for the trivia console pages, mirroring
// pages/tasks/common.ts.
//
// The phase and action vocabularies live in ../../triviaPhases so the phone
// bundle shares them rather than keeping a second copy.
import { ACTION, PHASE, type Action, type Phase } from '../../triviaPhases';

export { ACTION, PHASE };
export type { Action, Phase };

export interface TriviaSettings {
  title: string;
  board_rows: number;
  board_columns: number;
  cell_values: number[];
  token_values: number[];
  final_wager: boolean;
  answer_seconds: number;
  reveal_seconds: number;
  bet_seconds: number;
  // The blind-bet clock in front of the final's question. Only meaningful
  // with final_wager on — the phase never opens otherwise.
  wager_seconds: number;
  // The beat the room still gets once every table is in. The deadline is
  // pulled in to this rather than the phase closing on the spot, so the table
  // that acted last gets the look-at-it beat everyone else had. 0 means close
  // immediately, which is what the game did before this existed.
  grace_seconds: number;
  // Whether this game may draw on questions an earlier night already asked.
  // Off by default: a question the room has heard is not a question, and the
  // regulars are the people most likely to notice.
  repeat_questions: boolean;
  // How many boards the night plays before the final. One is the game as it
  // shipped; two is the pub hour, with a break in between. Each round has its
  // own categories and is worth double the one before it.
  board_rounds: number;
}

export interface TriviaGame {
  id: string;
  name: string;
  title: string;
  phase: Phase;
  join_url: string;
  // Same destination, a third of the characters — what the QR encodes and
  // what the TV prints.
  short_url: string;
  // The stable address — always shows the newest game. This is the one to put
  // on the screen.
  screen_url: string;
  // Pins this one game. The exception, not the default.
  tv_url: string;
  teams: number;
  cells: number;
  played: number;
  leader: string;
  created_at: string;
  settings: TriviaSettings;
  // Set when a settings change redrew the board and the bank could not
  // fill the new shape. The board is empty until the host fixes one or the
  // other.
  board_error?: string;
}

// One bar of the setup page's category picker. `unused` means FRESH — no
// round in a surviving game has asked it — which is not the same as "not on
// a previous board": a board that was built and never finished spent
// nothing. With repeats allowed on the game, every question counts as
// available and this equals `total`.
export interface TopicCount {
  key: string;
  label: string;
  total: number;
  unused: number;
}

export interface HostTeam {
  id: string;
  name: string;
  score: number;
  eligible: boolean;
  answered: boolean;
  stakeLocked: boolean;
  chipsPlaced: number;
}

// One tile as the SETUP page sees it: the cell plus the question behind it.
//
// This is NOT HostCell. HostCell rides on every SSE frame and shares its shape
// with the TV and the phones, so the prompts stay off it; these are fetched
// once, by request, on the page where the host is reading their own board
// before the doors open.
export interface BoardQuestion {
  id: string;
  // Which board this tile belongs to, zero-based. The setup page shows every
  // round; the room only ever sees the one in play.
  round: number;
  col: number;
  row: number;
  topic: string;
  points: number;
  played: boolean;
  prompt: string;
  answer: string;
  // How many other questions this column could swap in. Zero means the Swap
  // button would fail, so it is disabled and says why.
  spares: number;
}

export interface HostCell {
  id: string;
  col: number;
  row: number;
  topic: string;
  points: number;
  played: boolean;
}

export interface HostSlot {
  id: string;
  pos: number;
  value: number | null;
  label: string;
  teams: string[];
  pot: number;
  chips: { team: string; amount: number }[];
}

export interface HostRound {
  id: string;
  isFinal: boolean;
  ordinal: number;
  points: number;
  // Empty during `wager`: the prompt is withheld from every surface, the
  // console included, so the host cannot read it out early by accident.
  text: string;
  category: string;
  answered: number;
  eligible: number;
}

export interface HostScoring {
  correctValue: number;
  correctText: string;
  winningSlot: string;
  deltas: Record<string, number>;
  boardPoints: Record<string, number>;
  betDeltas: Record<string, number>;
}

// The round that was scored most recently — which is NOT the round in play.
// It survives the host pressing next (which clears the current round), and it
// is the only thing that knows who picks the next category. Host frame only.
export interface HostLastRound {
  ordinal: number;
  isFinal: boolean;
  points: number;
  text: string;
  correctValue: number;
  correctText: string;
  winningSlot: string;
  winningLabel: string;
  // Parallel arrays, same order — the server aggregates them together.
  winners: string[];
  winnerIds: string[];
  deltas: Record<string, number>;
  boardPoints: Record<string, number>;
  betDeltas: Record<string, number>;
}

// Who picks the next category, and why it is theirs. Decided by the server —
// the first one is drawn at random and announced on the TV with a wheel,
// after that it follows the winning card. Every surface reads this field
// rather than re-deriving the rule, so none of them can name a different
// table.
export interface Picker {
  teamId: string;
  name: string;
}

export type PickerReason = '' | 'drawn' | 'wrote_winner' | 'lowest';

export interface HostFrame {
  version: number;
  game: string;
  title: string;
  phase: Phase;
  serverNow: number;
  deadlineMs: number;
  finalWager: boolean;
  // The night's position as a human says it: one-based, and the final counts
  // as a round. Two boards plus a final is "round 1 of 3".
  roundNumber: number;
  roundCount: number;
  // The round in play IS the final, so print the word, not the number.
  isFinal: boolean;
  teams: HostTeam[];
  board: HostCell[];
  round: HostRound | null;
  slots: HostSlot[];
  scoring: HostScoring | null;
  lastRound: HostLastRound | null;
  answer: { value: number; text: string } | null;
  tokens: number[];
  progress: { cellsPlayed: number; cellsTotal: number; finalPlayed: boolean };
  picker: Picker | null;
  pickerReason: PickerReason;
}

// A dataset is a named set of questions. It is the only "set of questions"
// concept: an upload creates one, the shipped starter pack is seeded as one,
// and a game draws its board from one or more of them.
export interface Dataset {
  id: string;
  name: string;
  notes: string;
  builtin_key: string;
  questions: number;
  // How many of those no game has asked yet — the number that says whether
  // this set still has a night in it.
  fresh: number;
  topics: number;
  created_at: string;
  updated_at: string;
}

// A pack Kit ships. Loading one creates an ordinary dataset — after that it
// is yours to rename, replace or delete.
export interface BuiltinPack {
  key: string;
  name: string;
  notes: string;
}

export interface ImportReport {
  dataset_id: string;
  imported: number;
  updated: number;
  skipped_duplicates: number;
  errors: { line: number; message: string }[];
  truncated: boolean;
  topics: TopicCount[];
}

export const PHASE_LABEL: Record<Phase, string> = {
  [PHASE.SETUP]: 'Teams joining',
  [PHASE.LOBBY]: 'Teams joining',
  [PHASE.BOARD]: 'On the board',
  [PHASE.INTERMISSION]: 'Break',
  [PHASE.WAGER]: 'Wagers in',
  [PHASE.QUESTION]: 'Answering',
  // Named for what the host does next, not for what just happened: the cards
  // are up and betting is the thing that has not started yet.
  [PHASE.REVEAL]: 'Cards up, betting next',
  [PHASE.BETTING]: 'Placing bets',
  [PHASE.SCORING]: 'Scored',
  [PHASE.PODIUM]: 'Finished',
};

// Every cell played. It decides what the primary button offers and whether
// there is still a category for the last round's winner to pick.
export function boardIsEmpty(frame: HostFrame): boolean {
  return frame.progress.cellsTotal > 0 && frame.progress.cellsPlayed === frame.progress.cellsTotal;
}

// The label on the one big primary button, per phase. Naming it by what
// happens next is what lets the host drive the night without reading the
// screen — the button is always in the same place and always says the thing
// they are about to do.
export function primaryAction(phase: Phase, boardEmpty: boolean, finalWager: boolean, finalPlayed: boolean, skipReveal = false):
  { action: Action; label: string } | null {
  switch (phase) {
    // A game is joinable from the moment it exists, so there is no state to
    // announce before starting: the host's controls are start and end.
    case PHASE.SETUP:
    case PHASE.LOBBY: return { action: ACTION.START, label: 'Start the game' };
    case PHASE.BOARD:
      if (boardEmpty && finalWager && !finalPlayed) return { action: ACTION.FINAL, label: 'Final question' };
      if (boardEmpty) return { action: ACTION.FINISH, label: 'Go to the podium' };
      return null; // waiting for the host to pick a cell
    // Nothing is on a clock during the break. The host decides when the room
    // has finished its drink, which is the entire point of the phase.
    case PHASE.INTERMISSION: return { action: ACTION.RESUME, label: 'Start the next round' };
    // The wager is the one phase whose primary button reveals nothing and
    // scores nothing: it puts the question on the wall. Named for that.
    case PHASE.WAGER: return { action: ACTION.ASK, label: 'Ask the question' };
    case PHASE.QUESTION: return { action: ACTION.REVEAL, label: skipReveal ? 'Reveal and open betting' : 'Reveal answers' };
    case PHASE.REVEAL: return { action: ACTION.OPEN_BETTING, label: 'Open betting' };
    case PHASE.BETTING: return { action: ACTION.SCORE, label: 'Score the round' };
    case PHASE.SCORING: return { action: ACTION.NEXT, label: 'Next' };
    case PHASE.PODIUM: return null;
  }
}

// The SECOND button, when the night has a choice to offer rather than a next
// step to take.
//
// Kept apart from primaryAction on purpose: that one is the thing the host
// presses without reading, always in the same place, and it must not become a
// pair of equal options. This is the one that only appears at the two moments
// a host is genuinely asked a question by the room — the break, and an
// emptied board with the final still to come.
export function extraAction(phase: Phase, boardEmpty: boolean):
  { action: Action; label: string } | null {
  if (phase === PHASE.INTERMISSION) return { action: ACTION.ADD_ROUND, label: 'Add another round' };
  if (phase === PHASE.BOARD && boardEmpty) {
    return { action: ACTION.ADD_ROUND, label: 'Add another round' };
  }
  return null;
}

// The reason in the host's words, appended to the pick line. Short, because
// it is read aloud over a room: the sentence has to end before anybody stops
// listening.
export function pickerWhy(reason: PickerReason): string {
  switch (reason) {
    case 'drawn': return 'drawn at random';
    case 'wrote_winner': return 'wrote the winning answer';
    case 'lowest': return 'lowest score picks';
    default: return '';
  }
}

// How the host says where the night is. The final is named rather than
// numbered — "Final" tells a room more than "round 3 of 3" does.
export function roundLabel(frame: { roundNumber: number; roundCount: number; isFinal: boolean }): string {
  if (frame.isFinal) return 'Final round';
  if (frame.roundCount <= 1) return '';
  return `Round ${frame.roundNumber} of ${frame.roundCount}`;
}

// Whether two settings describe the same game.
//
// This is what decides if there is anything to SAVE, and it compares values
// rather than identity on purpose: every reload from the server hands back a
// fresh object that is field-for-field what the form already holds, and an
// identity check treats that as an edit.
export function sameSettings(a: TriviaSettings, b: TriviaSettings): boolean {
  const numbers = [
    'board_rows', 'board_columns', 'answer_seconds', 'reveal_seconds',
    'bet_seconds', 'wager_seconds', 'grace_seconds', 'board_rounds',
  ] as const;
  if (a.title !== b.title) return false;
  if (a.final_wager !== b.final_wager || a.repeat_questions !== b.repeat_questions) return false;
  if (numbers.some((k) => a[k] !== b[k])) return false;
  const lists = ['cell_values', 'token_values'] as const;
  return lists.every((k) =>
    a[k].length === b[k].length && a[k].every((v, i) => v === b[k][i]));
}

// Roughly how long a night of this shape runs, in minutes.
//
// The reason this exists on the setup page at all: a host is not choosing
// "5 columns, 2 rows, 1 round", they are choosing "an hour". Three abstract
// numbers do not answer that question, and the only way to find out used to
// be to run the night and see.
//
// HONEST ABOUT ITS ASSUMPTIONS, because a confident wrong number is worse
// than none. The clocks are exact — they come from these same settings — but
// the human time between them is an estimate: reading the answer out, the
// scores landing, whoever picks taking a moment to pick. Thirty seconds a
// question is what that costs in a room that is enjoying itself, and it is
// the single biggest source of error here.
const SECONDS_OF_TALK_PER_QUESTION = 30;
const MINUTES_PER_BREAK = 8;

export function estimateMinutes(s: TriviaSettings): { minutes: number; questions: number } {
  const perQuestion =
    s.answer_seconds + s.reveal_seconds + s.bet_seconds + SECONDS_OF_TALK_PER_QUESTION;
  const rounds = Math.max(1, s.board_rounds);
  const questions = s.board_columns * s.board_rows * rounds;
  let seconds = questions * perQuestion;
  // The final adds its blind-wager phase in front of an otherwise ordinary
  // question.
  if (s.final_wager) seconds += s.wager_seconds + perQuestion;
  // Breaks sit BETWEEN boards, so there is one fewer than there are rounds.
  seconds += (rounds - 1) * MINUTES_PER_BREAK * 60;
  return { minutes: Math.round(seconds / 60), questions };
}

export function money(n: number): string {
  const neg = n < 0;
  return (neg ? '-$' : '$') + Math.abs(n).toLocaleString('en-US');
}

// defaultSettings is the shipped game, mirrored from the server's
// DefaultSettings so a new game is created with the same shape the docs and
// the host's card describe: two boards of 5 categories x 2 rows, twenty
// questions at $100 then $200 a cell, two chips at $100/$200, and a final.
export function defaultSettings(): TriviaSettings {
  return {
    title: '',
    board_rows: 2,
    board_columns: 5,
    cell_values: [100, 200],
    token_values: [100, 200],
    final_wager: true,
    answer_seconds: 60,
    reveal_seconds: 0,
    bet_seconds: 45,
    wager_seconds: 30,
    grace_seconds: 5,
    repeat_questions: false,
    // Two boards and a final — Jeopardy's shape, and about an hour.
    board_rounds: 2,
  };
}
