import { ChatEvent } from '@chat/events';
import { readSSE } from '@chat/sse';
import {
  API_BASE,
  CSRF_HEADER,
  apiDelete,
  apiGet,
  apiPatch,
  apiPost,
  apiPut,
  errorMessage,
  loginRedirect,
} from './client';
import {
  PosterGenerateEvent,
  PosterGenerateStage,
  type Poster,
  type PosterBuiltinVisibility,
  type PosterDetail,
  type PosterForEvent,
  type PosterGenerateResult,
  type PosterPhoto,
  type PosterPhotoPatch,
  type PosterPhotoStatus,
  type ChatHistoryTurn,
  type PosterPhotosPayload,
  type PosterSyncResult,
  type PosterTemplate,
  type PosterTemplateDetail,
  type PosterTemplateStatus,
  type PosterTemplateVersion,
  type PosterUpdateResult,
  type PosterVersion,
  type PostersSettings,
  type PostersSettingsBody,
} from './posters.types';

// generatePosterOptions runs one option batch for an event. The server
// streams progress over SSE (copy, photo, rendering) so the caller can say
// where the twenty seconds are going; the done frame carries the batch.
export async function generatePosterOptions(
  eventId: string,
  more: boolean,
  onProgress?: (stage: PosterGenerateStage) => void,
): Promise<PosterGenerateResult> {
  const r = await fetch(`${API_BASE}/posters/generate`, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', [CSRF_HEADER]: '1' },
    body: JSON.stringify({ event_id: eventId, more }),
  });
  if (r.status === 401) loginRedirect();
  if (!r.ok) throw new Error(await errorMessage(r));
  for await (const frame of readSSE(r)) {
    let data: unknown;
    try {
      data = JSON.parse(frame.data);
    } catch {
      continue;
    }
    if (frame.event === PosterGenerateEvent.Progress) {
      onProgress?.((data as { stage: PosterGenerateStage }).stage);
    } else if (frame.event === ChatEvent.Error) {
      throw new Error((data as { message?: string }).message ?? 'could not generate options');
    } else if (frame.event === ChatEvent.Done) {
      return data as PosterGenerateResult;
    }
  }
  throw new Error('the server stopped before the options were ready');
}

export const postersApi = {
  // --- Posters ---
  listPosters: () => apiGet<{ posters: Poster[]; renderer_ready: boolean }>('/posters'),
  posterForEvent: (eventId: string) =>
    apiGet<PosterForEvent>(`/posters/for-event/${encodeURIComponent(eventId)}`),
  getPoster: (id: string) => apiGet<PosterDetail>(`/posters/${encodeURIComponent(id)}`),
  deletePoster: (id: string) => apiDelete<void>(`/posters/${encodeURIComponent(id)}`),
  // Pick puts the version on the event (portrait PNG to the event's poster).
  pickPoster: (id: string, versionId: string) =>
    apiPost<{ poster: Poster }>(`/posters/${encodeURIComponent(id)}/pick`, {
      version_id: versionId,
    }),
  // Current is undo/redo/branch: the next edit descends from it.
  setPosterCurrent: (id: string, versionId: string) =>
    apiPost<{ current: PosterVersion }>(`/posters/${encodeURIComponent(id)}/current`, {
      version_id: versionId,
    }),
  updatePosterFacts: (id: string) =>
    apiPost<PosterUpdateResult>(`/posters/${encodeURIComponent(id)}/update-facts`),
  savePosterAsTemplate: (id: string) =>
    apiPost<{ template: PosterTemplate }>(`/posters/${encodeURIComponent(id)}/save-template`),
  posterRenderURL: (id: string, versionId: string, format?: string, download?: boolean) => {
    const q = new URLSearchParams();
    if (format) q.set('format', format);
    if (download) q.set('download', '1');
    const qs = q.toString();
    return `${API_BASE}/posters/${id}/versions/${versionId}/render${qs ? `?${qs}` : ''}`;
  },

  listPosterTemplates: (status?: PosterTemplateStatus | '') =>
    apiGet<{ templates: PosterTemplate[] }>(
      `/posters/templates${status ? `?status=${encodeURIComponent(status)}` : ''}`,
    ),
  createPosterTemplate: (body: { name?: string; parent_template_id?: string } = {}) =>
    apiPost<{ template: PosterTemplate }>('/posters/templates', body),
  getPosterTemplate: (id: string) =>
    apiGet<PosterTemplateDetail>(`/posters/templates/${encodeURIComponent(id)}`),
  // Tenant templates take draft/active/archived; built-ins take hidden/visible.
  setPosterTemplateStatus: (id: string, status: PosterTemplateStatus | PosterBuiltinVisibility) =>
    apiPost<{ template: PosterTemplate }>(
      `/posters/templates/${encodeURIComponent(id)}/status`,
      { status },
    ),
  renamePosterTemplate: (id: string, name: string) =>
    apiPost<void>(`/posters/templates/${encodeURIComponent(id)}/rename`, { name }),
  rollbackPosterTemplate: (id: string, versionId: string) =>
    apiPost<{ version: PosterTemplateVersion }>(
      `/posters/templates/${encodeURIComponent(id)}/rollback`,
      { version_id: versionId },
    ),
  // Renders the template with real content: the named event, else the next
  // upcoming one. `v` is a cache-buster (the template's updated_at).
  posterTemplateRenderURL: (
    id: string,
    opts: { event_id?: string; format?: string; ground?: string; v?: string } = {},
  ) => {
    const q = new URLSearchParams();
    if (opts.event_id) q.set('event_id', opts.event_id);
    if (opts.format) q.set('format', opts.format);
    if (opts.ground) q.set('ground', opts.ground);
    if (opts.v) q.set('v', opts.v);
    const qs = q.toString();
    return `${API_BASE}/posters/templates/${id}/render${qs ? `?${qs}` : ''}`;
  },
  posterTemplateReferenceURL: (id: string) => `${API_BASE}/posters/templates/${id}/reference`,
  posterChatHistory: (id: string) => apiGet<{ turns: ChatHistoryTurn[] }>(`/posters/${encodeURIComponent(id)}/chat/history`),
  templateChatHistory: (id: string) =>
    apiGet<{ turns: ChatHistoryTurn[] }>(`/posters/templates/${encodeURIComponent(id)}/chat/history`),

  listPosterPhotos: (opts: { status?: PosterPhotoStatus | ''; folder?: string; limit?: number; offset?: number } = {}) => {
    const q = new URLSearchParams();
    if (opts.status) q.set('status', opts.status);
    if (opts.folder) q.set('folder', opts.folder);
    if (opts.limit) q.set('limit', String(opts.limit));
    if (opts.offset) q.set('offset', String(opts.offset));
    const qs = q.toString();
    return apiGet<PosterPhotosPayload>(`/posters/photos${qs ? `?${qs}` : ''}`);
  },
  updatePosterPhoto: (id: string, body: PosterPhotoPatch) =>
    apiPatch<{ photo: PosterPhoto }>(`/posters/photos/${encodeURIComponent(id)}`, body),
  posterPhotoImageURL: (id: string, size = 1024) =>
    `${API_BASE}/posters/photos/${id}/image?size=${size}`,

  postersSettings: () => apiGet<PostersSettings>('/posters/settings'),
  savePostersSettings: (body: PostersSettingsBody) =>
    apiPut<PostersSettings>('/posters/settings', body),
  postersSyncNow: () => apiPost<{ result: PosterSyncResult }>('/posters/settings/sync'),
  postersRederive: () => apiPost<PostersSettings>('/posters/settings/rederive'),
};
