// The trivia phase and action vocabularies, as real constants.
//
// ONE DEFINITION, shared by the host console and the player's phone, which
// are two bundles with two entry points and used to carry two hand-written
// copies of this union. They had already drifted: the phone's list was
// missing `intermission` entirely, so the break fell through its switch to a
// "Hold on" screen.
//
// The types are DERIVED from the constants rather than written beside them,
// so there is no second list to keep in step — add a member here and every
// exhaustive switch in both bundles fails to compile until it is handled.
// This mirrors the Go side, where Phase is a named type with named constants
// and the `exhaustive` linter enforces the same thing.
export const PHASE = {
  SETUP: 'setup',
  LOBBY: 'lobby',
  BOARD: 'board',
  // The break between board rounds.
  INTERMISSION: 'intermission',
  WAGER: 'wager',
  QUESTION: 'question',
  REVEAL: 'reveal',
  BETTING: 'betting',
  SCORING: 'scoring',
  PODIUM: 'podium',
} as const;

export type Phase = (typeof PHASE)[keyof typeof PHASE];

// The host's vocabulary. Mirrors the Action constants in service_live.go;
// these are the strings the one host endpoint dispatches on.
export const ACTION = {
  START: 'start',
  PICK_CELL: 'pick_cell',
  ASK: 'ask',
  REVEAL: 'reveal',
  OPEN_BETTING: 'open_betting',
  SCORE: 'score',
  NEXT: 'next',
  FINAL: 'final',
  EXTEND: 'extend',
  FINISH: 'finish',
  // Ends the break and opens the next board.
  RESUME: 'resume',
} as const;

export type Action = (typeof ACTION)[keyof typeof ACTION];

export const PICKER_REASON = {
  NONE: '',
  DRAWN: 'drawn',
  WROTE_WINNER: 'wrote_winner',
  LOWEST: 'lowest',
} as const;

export type PickerReason = (typeof PICKER_REASON)[keyof typeof PICKER_REASON];
