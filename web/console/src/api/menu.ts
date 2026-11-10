import { API_BASE, apiDelete, apiGet, apiPatch, apiPost, apiPut } from './client';
import type { TriviaFeedbackChannel } from './events.types';

// A kiosk board: one unattended screen. `key` is baked into the machine's
// browser homepage, so it is the stable half; `public_url` is what an admin
// copies onto the machine. `last_seen_at` is the only health signal there is
// — it is set by the screen's own polling.
// The workspace's menu board. One per workspace, read-only in the console:
// the tap list follows Untappd and the presentation is pushed from an AI
// client, so this page only answers "what URL do I paste into the screen?".
/** One priced container of a beer, as Untappd writes it. */
export interface MenuPour {
  size: string;
  label: string;
  price: string;
}

/** An item Kit adds to the Untappd menu — a can, a soda. */
export interface MenuItem {
  section: string;
  name: string;
  style?: string;
  abv?: string;
  notes?: string;
  pours?: MenuPour[];
  /** Which menu it is on; omitted means both. */
  show?: 'print' | 'board' | 'both';
}

/** Kit's standing edits to the Untappd menu. */
export interface MenuEdits {
  items: MenuItem[];
  hidden: { items: string[]; sections: string[] };
}

/** One line of the merged menu list. */
export interface MenuItemRow {
  name: string;
  source: 'untappd' | 'kit';
  style: string;
  price: string;
  size: string;
  show: '' | 'print' | 'board' | 'both';
  hidden: boolean;
  out: boolean;
  /** A Kit item's place in edits.items; -1 for Untappd rows. */
  index: number;
}

export interface MenuItems {
  sections: { name: string; hidden: boolean; items: MenuItemRow[] }[];
  edits: MenuEdits;
}

/** A change to the menu list: the same arguments set_menu_items takes. */
export interface MenuItemsChange {
  items?: MenuItem[];
  hide?: string[];
  unhide?: string[];
}

/** Everything about the printed menu the tap list cannot say. All optional. */
export interface MenuPrintConfig {
  brand?: string;
  title?: string;
  subtitle?: string;
  flight?: string;
  sizes?: string;
  foot_left?: string;
  foot_right?: string;
  hero?: string;
  notes?: Record<string, string>;
  /**
   * Section heading to a sentence printed under it. A heading named here that
   * is not on the tap list becomes a section of its own at the end — which is
   * how snacks reach a beer menu without a row and a price each.
   */
  blurbs?: Record<string, string>;
  /** The one beer boxed at the head of the printed menu. */
  featured?: MenuFeatured;
}

/**
 * The featured beer. `photo` is stored as "asset:<key>" or "none"; on save it
 * may also be "auto" (find one by name) or "photo:<id>" (a library photo,
 * which the server fetches and keeps).
 */
export interface MenuFeatured {
  beer: string;
  label?: string;
  text?: string;
  photo?: string;
}

/** A photo the featured box could use, best first. */
export interface MenuFeaturedPhoto {
  ref: string;
  label: string;
}

/** One synced beer, as the settings page shows it. Facts are read-only here —
 *  they follow the board — but the description is yours to write. */
export interface MenuPrintBeer {
  section: string;
  name: string;
  style: string;
  abv: string;
  price: string;
  note: string;
  /** True when the description was typed in Kit rather than cached from Untappd. */
  written: boolean;
}

/** The happy hour setting. Both the wall and Square are derived from it. */
export interface HappyHourConfig {
  enabled: boolean;
  /** mon..sun */
  days: string[];
  /** "15:00" */
  start: string;
  end: string;
  /** "2026-10-12", or empty for already running. */
  starts_on?: string;
  price_cents: number;
  /** The Square variation name the price applies to, e.g. "16oz". */
  size: string;
  beers: string[];
}

export interface HappyHourTap {
  name: string;
  price: string;
  size: string;
}

export interface HappyHour {
  config: HappyHourConfig;
  /** False until the first save: the form shows the defaults. */
  configured: boolean;
  /** Whether happy hour is on this minute, on the wall and in Square. */
  on_now: boolean;
  /** When it ends ("5pm", "Tue 5pm"); empty when only End now will end it. */
  until: string;
  timezone: string;
  /** What is on the board, offered as checkboxes. */
  taps: HappyHourTap[];
  /** Square was last built from the setting as it is now. */
  in_sync: boolean;
  synced_at: string | null;
  sync_ok: boolean;
  sync_log: string;
}

export interface HappyHourSyncResult {
  state: HappyHour;
  log: string;
  ok: boolean;
  applied: boolean;
}

export interface GlutenReduced {
  beers: string[];
  taps: string[];
}

export interface MenuPrint {
  config: MenuPrintConfig;
  /** Headings on the tap list right now, offered to the section pickers. */
  sections: string[];
  /** How many descriptions have been fetched from Untappd and stored. */
  notes_cached: number;
  print_url: string;
  /** The synced tap list — what will actually print. */
  beers: MenuPrintBeer[];
  /** Null until the first sync. The page leads with the button while it is. */
  synced_at: string | null;
}

export interface MenuBoard {
  name: string;
  public_url: string;
  /** Null until a tap list exists; the address works regardless. */
  updated_at: string | null;
  taps: number;
  panels: number;
  /** True before any tap list is set — the screen shows a placeholder. */
  empty: boolean;
  /** Untappd board being followed; blank when set by hand. */
  source: string;
  synced_at: string | null;
  sync_error?: string;
  /** Set when the stored payload no longer parses. */
  parse_error?: string;
}

export interface KioskUrlChange {
  url: string;
  replaced_at: string;
}

export interface KioskBoard {
  id: string;
  key: string;
  name: string;
  url: string;
  notes: string;
  public_url: string;
  last_seen_at: string | null;
  updated_at: string;
  /** What this board pointed at before, newest first. */
  recent_urls: KioskUrlChange[] | null;
}

/** A one-tap button for what a screen shows: one of Kit's own screen pages,
 * or (custom) one the workspace added. */
export interface KioskDestination {
  key: string;
  label: string;
  url: string;
  custom: boolean;
}

export interface KioskBoardInput {
  key?: string;
  name: string;
  url: string;
  notes: string;
}

export const menuApi = {
  menuBoard: () => apiGet<MenuBoard>('/menu/board'),
  menuPrint: () => apiGet<MenuPrint>('/menu/print'),
  saveMenuPrint: (body: MenuPrintConfig) =>
    apiPut<MenuPrint>('/menu/print', body),
  menuFeaturedPhotos: (beer: string) =>
    apiGet<{ candidates: MenuFeaturedPhoto[] }>(
      `/menu/print/featured/photos?beer=${encodeURIComponent(beer)}`,
    ),
  menuFeaturedPhotoUrl: (ref: string) =>
    `${API_BASE}/menu/print/featured/photo?ref=${encodeURIComponent(ref)}`,
  /** `missing` is what will print without a description. */
  menuPrintNotes: () =>
    apiGet<{ beers: MenuPrintBeer[]; missing: string[] }>('/menu/print/notes'),
  saveMenuPrintNote: (name: string, text: string) =>
    apiPut<{ beers: MenuPrintBeer[] }>('/menu/print/notes', { name, text }),
  menuItems: () => apiGet<MenuItems>('/menu/items'),
  saveMenuItems: (change: MenuItemsChange) => apiPut<MenuItems>('/menu/items', change),
  eightySix: () => apiGet<GlutenReduced>('/menu/86'),
  saveEightySix: (beers: string[]) => apiPut<GlutenReduced>('/menu/86', { beers }),
  // Same shape as trivia's ratings channel: the setting plus the picker.
  eightySixChannel: () => apiGet<TriviaFeedbackChannel>('/menu/86/channel'),
  saveEightySixChannel: (channelId: string) =>
    apiPut<TriviaFeedbackChannel>('/menu/86/channel', { channel_id: channelId }),
  glutenReduced: () => apiGet<GlutenReduced>('/menu/gluten-reduced'),
  saveGlutenReduced: (beers: string[]) =>
    apiPut<GlutenReduced>('/menu/gluten-reduced', { beers }),
  happyHour: () => apiGet<HappyHour>('/menu/happy-hour'),
  saveHappyHour: (body: HappyHourConfig) =>
    apiPut<HappyHour>('/menu/happy-hour', body),
  syncHappyHour: (apply: boolean) =>
    apiPost<HappyHourSyncResult>('/menu/happy-hour/sync', { apply }),
  happyHourNow: (on: boolean) =>
    apiPost<HappyHourSyncResult>('/menu/happy-hour/now', { on }),

  kioskBoards: () =>
    apiGet<{ boards: KioskBoard[]; destinations: KioskDestination[] }>('/kiosk/boards'),
  createKioskBoard: (body: KioskBoardInput) =>
    apiPost<KioskBoard>('/kiosk/boards', body),
  updateKioskBoard: (id: string, body: KioskBoardInput) =>
    apiPatch<KioskBoard>(`/kiosk/boards/${encodeURIComponent(id)}`, body),
  deleteKioskBoard: (id: string) =>
    apiDelete<void>(`/kiosk/boards/${encodeURIComponent(id)}`),
  createKioskButton: (body: { label: string; url: string }) =>
    apiPost<KioskDestination>('/kiosk/buttons', body),
  deleteKioskButton: (id: string) =>
    apiDelete<void>(`/kiosk/buttons/${encodeURIComponent(id)}`),
};
