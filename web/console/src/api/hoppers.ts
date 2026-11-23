import { apiDelete, apiGet, apiPost } from './client';
import type { Frame } from '../hoppers/api';

// --- Hoppers ---
//
// The host's surface. The game itself has one mutating endpoint for the host,
// `advance`: it carries the stage the host saw, so a double tap (or two hosts
// at once) is a 409 rather than a skipped heat.

export interface HoppersRules {
  rounds?: number;
  heatSize?: number;
  minFrogs?: number;
  flickSeconds?: number;
}

export interface HoppersPack {
  key: string;
  rules: HoppersRules;
  levels: { name: string; layout: string; theme: string; length: number }[] | null;
}

export interface HoppersGame {
  id: string;
  name: string;
  title: string;
  pack: string;
  phase: string;
  players: number;
  leader: string;
  created_at: string;
  play_url: string;
  join_url: string;
}

export interface HoppersHostInfo {
  canAdvance: boolean;
  advance: string;
  nextDeadline: number;
  config: { rounds: number; heatSize: number; minFrogs: number; flickTimeoutMs: number };
}

// The host's frame: the TV's view plus the controls.
export interface HoppersHostFrame extends Frame {
  host?: HoppersHostInfo;
  heatName: string;
  final: boolean;
}

export interface HoppersStage {
  phase: string;
  sub: string;
  round: number;
  heat: number;
}

export const hoppersApi = {
  hoppersPacks: () => apiGet<{ packs: HoppersPack[] }>('/hoppers/packs'),
  hoppersGames: () => apiGet<{ games: HoppersGame[] }>('/hoppers/games'),
  createHoppersGame: (body: { title: string; pack: string; rules: HoppersRules }) =>
    apiPost<HoppersGame>('/hoppers/games', body),
  deleteHoppersGame: (id: string) => apiDelete<void>(`/hoppers/games/${id}`),
  hoppersAdvance: (id: string, from: HoppersStage) =>
    apiPost<HoppersHostFrame>(`/hoppers/games/${id}/advance`, from),
  hoppersReclaim: (id: string, playerId: string) =>
    apiPost<{ code: string }>(`/hoppers/games/${id}/players/${playerId}/reclaim`),
};
