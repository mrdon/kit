import { apiDelete, apiGet, apiPatch, apiPost, apiPut } from './client';
import type { TriviaFeedbackChannel } from './events.types';

// --- Trivia ---
//
// The live host surface. Note there is exactly ONE mutating endpoint for the
// game itself (`triviaAction`): every host click is a guarded transition
// carrying the phase it was made from, so a stale click 409s instead of
// silently skipping a question.

export type {
  BoardQuestion,
  BuiltinPack,
  Dataset,
  TriviaGame,
  TriviaSettings,
  TopicCount,
  ImportReport,
  HostFrame,
} from '../pages/trivia/common';
import type {
  BoardQuestion as BoardQuestionT,
  TriviaGame as TriviaGameT,
  TriviaSettings as TriviaSettingsT,
  TopicCount as TopicCountT,
  HostFrame as HostFrameT,
  ImportReport as ImportReportT,
  Dataset as DatasetT,
  BuiltinPack as BuiltinPackT,
} from '../pages/trivia/common';

export const triviaApi = {
  // --- Trivia ---
  triviaGames: () => apiGet<{ games: TriviaGameT[] }>('/trivia/games'),
  triviaGame: (id: string) =>
    apiGet<{
      game: TriviaGameT;
      topics: TopicCountT[];
      datasets: DatasetT[];
      selected: string[];
      state: HostFrameT;
      // What each tile will actually ask. Host-only, and fetched with the
      // page rather than ridden in on the SSE frames.
      cells: BoardQuestionT[];
    }>(`/trivia/games/${id}`),
  // Omitting settings means "same as last time" — the server carries the
  // previous game's setup forward.
  createTriviaGame: (settings?: TriviaSettingsT) =>
    apiPost<TriviaGameT>('/trivia/games', settings ? { settings } : {}),
  updateTriviaGame: (id: string, settings: TriviaSettingsT) =>
    apiPatch<TriviaGameT>(`/trivia/games/${id}`, { settings }),
  deleteTriviaGame: (id: string) => apiDelete<void>(`/trivia/games/${id}`),
  deleteAllTriviaGames: () => apiDelete<{ deleted: number }>('/trivia/games'),
  buildTriviaBoard: (id: string, topics: string[], auto: boolean) =>
    apiPost<HostFrameT>(`/trivia/games/${id}/board`, { topics, auto }),
  // Rotate one tile's question out for another in the same column, leaving
  // the rest of the board alone. Comes back with the whole board because
  // spending a spare in one column changes what its other cells have left.
  swapTriviaCell: (gameId: string, cellId: string) =>
    apiPost<{ cells: BoardQuestionT[] }>(
      `/trivia/games/${gameId}/board/cells/${cellId}/swap`, {}),
  // The single host action endpoint. from_phase is what makes a double click
  // a 409 rather than a skipped question.
  triviaAction: (id: string, body: Record<string, unknown>) =>
    apiPost<HostFrameT>(`/trivia/games/${id}/action`, body),
  triviaReclaim: (gameId: string, teamId: string) =>
    apiPost<{ code: string }>(`/trivia/games/${gameId}/teams/${teamId}/reclaim`, {}),
  triviaQuestions: () =>
    apiGet<{ total: number; topics: TopicCountT[]; datasets: DatasetT[]; packs: BuiltinPackT[] }>(
      '/trivia/questions'),
  // One click, no download-and-re-upload. Idempotent: the import upserts on
  // the folded prompt, so loading it twice adds nothing.
  loadTriviaPack: (key: string) =>
    apiPost<ImportReportT>(`/trivia/questions/packs/${key}`, {}),
  triviaFeedbackChannel: () =>
    apiGet<TriviaFeedbackChannel>('/trivia/feedback-channel'),
  saveTriviaFeedbackChannel: (channelId: string) =>
    apiPut<TriviaFeedbackChannel>('/trivia/feedback-channel', { channel_id: channelId }),
  triviaDatasets: () => apiGet<{ datasets: DatasetT[] }>('/trivia/datasets'),
  renameTriviaDataset: (id: string, name: string, notes: string) =>
    apiPatch<{ datasets: DatasetT[] }>(`/trivia/datasets/${id}`, { name, notes }),
  deleteTriviaDataset: (id: string) => apiDelete<void>(`/trivia/datasets/${id}`),
  // An empty list means "every dataset" — that is what keeps a game playable
  // when the set it pointed at is later deleted.
  setTriviaGameDatasets: (gameId: string, datasetIds: string[]) =>
    apiPut<{ selected: string[]; topics: TopicCountT[] }>(
      `/trivia/games/${gameId}/datasets`, { dataset_ids: datasetIds }),
};
