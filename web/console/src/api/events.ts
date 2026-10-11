import { API_BASE, CSRF_HEADER, apiDelete, apiGet, apiPatch, apiPost, apiPut, parse } from './client';
import type {
  ChannelsPayload,
  EventChannel,
  EventInput,
  EventRecord,
  EventsDayNotice,
  EventsMeta,
  EventsReconcilePlan,
  EventsSettings,
  EventsSettingsSummary,
  EventsSiteStatus,
  EventsStaff,
  PromoPayload,
} from './events.types';

export const eventsApi = {
  eventsPromo: () => apiGet<PromoPayload>('/events/promo'),
  markEventPromo: (body: {
    event_id: string;
    channel_id: string;
    step_key: string;
    // 'todo' un-ticks, which DELETES the row rather than storing a state.
    status: 'todo' | 'done' | 'ignored';
    url?: string;
    note?: string;
  }) => apiPost<PromoPayload>('/events/promo/mark', body),

  listEventChannels: () => apiGet<ChannelsPayload>('/event-channels'),
  saveEventChannel: (body: Partial<EventChannel>) =>
    body.id
      ? apiPut<ChannelsPayload>(`/event-channels/${encodeURIComponent(body.id)}`, body)
      : apiPost<ChannelsPayload>('/event-channels', body),
  deleteEventChannel: (id: string) =>
    apiDelete<ChannelsPayload>(`/event-channels/${encodeURIComponent(id)}`),

  eventsMeta: () => apiGet<EventsMeta>('/events/meta'),
  listEvents: (opts: { status?: string; include_past?: boolean } = {}) => {
    const q = new URLSearchParams();
    if (opts.status) q.set('status', opts.status);
    if (opts.include_past) q.set('include_past', 'true');
    const qs = q.toString();
    return apiGet<{ events: EventRecord[]; settings: EventsSettingsSummary }>(
      `/events${qs ? `?${qs}` : ''}`,
    );
  },
  getEvent: (id: string) =>
    apiGet<{ event: EventRecord; occurrences: string[] | null; canonicalURL: string }>(
      `/events/${encodeURIComponent(id)}`,
    ),
  createEvent: (body: EventInput) => apiPost<{ event: EventRecord }>('/events', body),
  cloneEvent: (id: string, body: { starts_at?: string; title?: string } = {}) =>
    apiPost<{ event: EventRecord }>(`/events/${encodeURIComponent(id)}/clone`, body),
  updateEvent: (id: string, body: EventInput) =>
    apiPatch<{ event: EventRecord }>(`/events/${encodeURIComponent(id)}`, body),
  publishEvent: (id: string) =>
    apiPost<{ event: EventRecord; warnings: string[] | null }>(
      `/events/${encodeURIComponent(id)}/publish`,
    ),
  unpublishEvent: (id: string) =>
    apiPost<{ event: EventRecord }>(`/events/${encodeURIComponent(id)}/unpublish`),
  cancelEvent: (id: string) =>
    apiPost<{ event: EventRecord }>(`/events/${encodeURIComponent(id)}/cancel`),
  reopenEvent: (id: string) =>
    apiPost<{ event: EventRecord }>(`/events/${encodeURIComponent(id)}/reopen`),

  eventsSettings: () => apiGet<EventsSettings>('/events/settings'),
  saveEventsSettings: (body: {
    calendar_id?: string;
    timezone?: string;
    public_url_template?: string;
  }) => apiPut<{ settings: EventsSettings; warning: string }>('/events/settings', body),
  rotateEventsFeedToken: () =>
    apiPost<{ settings: EventsSettings; warning: string }>('/events/settings/feed-token'),
  eventsSyncNow: () => apiPost<{ message: string }>('/events/sync'),
  // Poster upload is multipart, so it bypasses the JSON helpers -- but it
  // still needs the CSRF header and the 401 bounce.
  uploadEventPoster: async (id: string, file: File) => {
    const body = new FormData();
    body.append('poster', file);
    const r = await fetch(`${API_BASE}/events/${id}/poster`, {
      method: 'POST',
      credentials: 'same-origin',
      headers: { [CSRF_HEADER]: '1' },
      body,
    });
    return parse<{ event: EventRecord }>(r);
  },
  deleteEvent: (id: string) => apiDelete<{ deleted: boolean }>(`/events/${id}`),
  deleteEventPoster: (id: string) =>
    apiDelete<{ event: EventRecord }>(`/events/${id}/poster`),
  // Cache-busted on the event's updated_at so a replaced poster shows at once.
  eventPosterURL: (id: string, v?: string) =>
    `${API_BASE}/events/${id}/poster${v ? `?v=${encodeURIComponent(v)}` : ''}`,

  eventsSiteStatus: () => apiGet<EventsSiteStatus>('/events/site'),
  eventsPublishSite: () => apiPost<EventsSiteStatus>('/events/site/publish'),
  eventsReconcile: (apply: boolean) =>
    apiPost<EventsReconcilePlan>(`/events/reconcile${apply ? '?apply=true' : ''}`),

  eventsStaff: () => apiGet<EventsStaff>('/events/staff'),
  // An empty slack_user_id clears the mapping. Returns the whole page payload
  // so a save cannot leave the two dropdowns showing stale pairings.
  saveEventsStaffMapping: (body: {
    square_team_member_id: string;
    slack_user_id: string;
  }) => apiPut<EventsStaff>('/events/staff', body),
  // An empty channel_id turns notices off. Returns the whole page payload so
  // the picker cannot show a stale selection after a change.
  saveEventsNoticeChannel: (body: { channel_id: string }) =>
    apiPut<EventsStaff>('/events/staff/channel', body),
  previewEventsNotices: () =>
    apiPost<{ notice: EventsDayNotice | null; message: string }>(
      '/events/staff/preview',
    ),
  sendEventsNotices: () =>
    apiPost<{ message: string; staff: EventsStaff }>('/events/staff/send'),
};
