import { API_BASE, CSRF_HEADER, apiDelete, apiGet, apiPatch, apiPost, apiPut, parse } from './client';

// --- COGS: tanks, packaged beer, the brewer's log ---
//
// One read (`state`) feeds every brewer screen; every write is an action
// posted to one endpoint. The server owns the ledger rules (over-draws,
// blends, who may undo what) and answers with the history line to show.

export type VesselKind = 'fermenter' | 'brite' | 'serving' | 'barrel';
export type VesselArea = 'brewhouse' | 'chiller' | 'barrels';
export type FormatKind = 'keg' | 'can' | 'bottle';

export interface BrewVessel {
  id: string;
  name: string;
  kind: VesselKind;
  area: VesselArea;
  size_bbl: number;
  sort: number;
  retired_at?: string | null;
  batch_id: string | null;
  volume_bbl: number;
  stage: string;
  stage_at: string | null;
  filled_at: string | null;
  dirty: boolean;
  on_tap: boolean;
}

export type TileBadge = 'due' | 'dirty' | 'empty' | 'on_tap' | '';

export interface BrewTile extends BrewVessel {
  beer_id: string | null;
  beer: string;
  batch_code: string;
  brewed_on: string;
  stage_label: string;
  age_label: string;
  next: string;
  badge: TileBadge;
  ready_on?: string;
  next_action?: string;
}

export interface BrewBeer {
  id: string;
  name: string;
  code: string;
  aliases: string[];
  family: string;
  style: string;
  active: boolean;
  staple: boolean;
  needs_setup: boolean;
  ferment_days: number;
  dry_hop: boolean;
  dry_hop_days: number;
  condition_days: number;
  brite_days: number;
  uses_brite: boolean;
  keg_shelf_weeks: number;
  pkg_shelf_weeks: number;
}

export interface BrewFormat {
  id: string;
  name: string;
  kind: FormatKind;
  bbl_per_unit: number;
  units_per_case: number;
  returnable: boolean;
  sort: number;
  retired_at?: string | null;
}

export interface BrewSettings {
  partial_fullness: number;
  barrel_bbl: number;
  brewhouse_bbl: number;
  count_interval_days: number;
}

export interface WeekItem {
  kind: 'brew' | 'clean' | 'due' | 'soon' | 'count';
  text: string;
  when?: string;
  action?: string;
  vessel_id?: string;
  plan_entry_id?: string;
  due: boolean;
}

export interface PackagedRow {
  beer_id: string;
  beer: string;
  format_id: string;
  format: string;
  format_kind: FormatKind;
  units: number;
  bbl: number;
  oldest_on?: string;
  age_weeks: number;
  past_shelf: boolean;
  lots: { batch_code: string; packaged_on: string; units: number }[];
}

export interface ScheduleBar {
  kind: 'in_tank' | 'planned' | 'clean' | 'free';
  label: string;
  start: number;
  end: number;
  plan_entry_id?: string;
  why?: string;
  clash?: boolean;
}

export interface ScheduleLane {
  vessel_id: string;
  name: string;
  bars: ScheduleBar[];
}

export interface PlanEntry {
  id: string;
  beer_id: string;
  vessel_id: string;
  brew_on: string;
  volume_bbl: number;
  why: string;
  batch_id: string | null;
}

export interface BrewState {
  today: string;
  vessels: BrewTile[];
  this_week: WeekItem[];
  packaged: PackagedRow[];
  schedule: ScheduleLane[];
  plan: PlanEntry[];
  beers: BrewBeer[];
  formats: BrewFormat[];
  settings: BrewSettings;
  last_count: string | null;
}

export interface HistoryEntry {
  action_id: string;
  at: string;
  kind: string;
  text: string;
  by: string;
  reversed: boolean;
  vessel_ids: string[];
  can_undo: boolean;
}

// A packaged quantity: kegs as full and partial, cans and bottles as cases
// plus loose units.
export interface LineInput {
  beer_id?: string;
  format_id: string;
  batch_id?: string;
  units?: number;
  full?: number;
  partial?: number;
  cases?: number;
  loose?: number;
  none?: boolean;
}

export interface ActionInput {
  kind: string;
  date?: string;
  vessel_id?: string;
  to_vessel_id?: string;
  to_vessel_ids?: string[];
  beer_id?: string;
  new_beer_name?: string;
  batch_id?: string;
  volume_bbl?: number;
  volume_in_bbl?: number;
  gallons?: number;
  reading_bbl?: number;
  all?: boolean;
  empty?: boolean;
  lines?: LineInput[];
  line_loss_units?: number;
  take?: LineInput;
  keep_rest?: boolean;
  reason?: string;
  note?: string;
  added?: string;
  stage?: string;
  on_tap?: boolean;
  brewed_on?: string;
  source?: string;
  photo_id?: string;
  plan_entry_id?: string;
}

export interface ActionResult {
  action_id: string;
  message: string;
}

export interface BrewSetup {
  vessels: BrewVessel[];
  beers: BrewBeer[];
  formats: BrewFormat[];
  settings: BrewSettings;
}

export interface VesselInput {
  name: string;
  kind: VesselKind;
  area?: VesselArea;
  size_bbl: number;
  sort?: number;
  retired?: boolean;
}

export interface FormatInput {
  name: string;
  kind: FormatKind;
  bbl_per_unit: number;
  units_per_case: number;
  returnable: boolean;
  sort?: number;
  retired?: boolean;
}

export interface PlanInput {
  beer_id: string;
  vessel_id: string;
  brew_on: string;
  volume_bbl: number;
  why: string;
}

export interface QueueItem {
  id: string;
  kind: 'new_beer' | 'plan_flag' | 'count_variance' | string;
  title: string;
  body: string;
  beer_id: string | null;
  plan_entry_id: string | null;
  action_id: string | null;
  created_label: string;
  created_at: string;
}

// uploadBrewPhoto posts the raw image; the brew that follows links it.
async function uploadBrewPhoto(blob: Blob): Promise<{ id: string }> {
  const r = await fetch(`${API_BASE}/cogs/photos`, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': blob.type || 'image/jpeg', [CSRF_HEADER]: '1' },
    body: blob,
  });
  return parse<{ id: string }>(r);
}

export const cogsApi = {
  brewState: () => apiGet<BrewState>('/cogs/state'),
  brewHistory: (vesselId?: string, limit = 50) =>
    apiGet<{ entries: HistoryEntry[] }>(
      `/cogs/history?limit=${limit}${vesselId ? `&vessel_id=${encodeURIComponent(vesselId)}` : ''}`,
    ),
  brewRecord: (body: ActionInput) => apiPost<ActionResult>('/cogs/actions', body),
  brewUndo: (actionId: string) => apiPost<ActionResult>(`/cogs/actions/${actionId}/undo`),
  brewFlagPlan: (id: string, note: string) => apiPost<void>(`/cogs/plan/${id}/flag`, { note }),
  brewUploadPhoto: uploadBrewPhoto,
  brewPhotoUrl: (id: string) => `${API_BASE}/cogs/photos/${id}`,

  brewSetup: () => apiGet<BrewSetup>('/cogs/setup'),
  brewSaveVessel: (id: string | null, body: VesselInput) =>
    id ? apiPatch<{ id: string }>(`/cogs/vessels/${id}`, body) : apiPost<{ id: string }>('/cogs/vessels', body),
  brewAddVessels: (body: VesselInput & { prefix: string; from: number; to: number }) =>
    apiPost<{ added: number }>('/cogs/vessels/bulk', body),
  brewSaveBeer: (id: string | null, body: Partial<BrewBeer>) =>
    id ? apiPatch<{ id: string }>(`/cogs/beers/${id}`, body) : apiPost<{ id: string }>('/cogs/beers', body),
  brewAddBeers: (names: string[]) => apiPost<{ added: number }>('/cogs/beers/bulk', { names }),
  brewSaveFormat: (id: string | null, body: FormatInput) =>
    id ? apiPatch<{ id: string }>(`/cogs/formats/${id}`, body) : apiPost<{ id: string }>('/cogs/formats', body),
  brewSaveSettings: (body: BrewSettings) => apiPut<void>('/cogs/settings', body),
  brewQueue: () => apiGet<{ items: QueueItem[] }>('/cogs/queue'),
  brewResolveQueue: (id: string) => apiPost<void>(`/cogs/queue/${id}/resolve`),
  brewSavePlan: (id: string | null, body: PlanInput) =>
    id ? apiPatch<{ id: string }>(`/cogs/plan/${id}`, body) : apiPost<{ id: string }>('/cogs/plan', body),
  brewCancelPlan: (id: string) => apiDelete<void>(`/cogs/plan/${id}`),
};
