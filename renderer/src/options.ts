// The option generator: one content object in, several posters out that
// differ in template, ground and photo, so there is a real choice to make.
// No model and no randomness: the same inputs always give the same options.
// The copy is never touched.
import type { Content, ImageRef, PhotoUse, PlannedTemplate } from "./types.ts";

export type { PlannedTemplate };

export type Candidate = {
  template: PlannedTemplate;
  ground: string;
  accent: string;
  photos: PhotoUse[];
};

export type PlanInput = {
  content: Content;
  templates: PlannedTemplate[];
  grounds: string[];
  accent: string;
  pool: PhotoUse[];
  exclude?: string[];
};

// Templates in weight order (most picked first, ties by the given order),
// dropping ones already shown unless that would leave nothing, and ones
// whose photo minimum the pool cannot meet or whose needs the content lacks.
export function eligibleTemplates(input: PlanInput): PlannedTemplate[] {
  const has = (field: string) => {
    const v = (input.content as unknown as Record<string, unknown>)[field];
    return Array.isArray(v) ? v.length > 0 : Boolean(v);
  };
  let ts = input.templates.filter((t) => t.meta.photos.min <= input.pool.length && t.meta.needs.every(has));
  const exclude = new Set(input.exclude ?? []);
  if (exclude.size) {
    const fresh = ts.filter((t) => !exclude.has(t.id));
    if (fresh.length) ts = fresh;
  }
  return [...ts].sort((a, b) => b.weight - a.weight);
}

// Every combination in spread order: templates cycle, the ground shifts each
// pass so a repeated template never comes back on the same ground, and photo
// templates take the next hero in turn.
export function plan(input: PlanInput): Candidate[] {
  const templates = eligibleTemplates(input);
  const grounds = input.grounds.length ? input.grounds : ["paper"];
  const out: Candidate[] = [];
  let photoTurn = 0;
  const total = templates.length * grounds.length;
  for (let i = 0; i < total; i++) {
    const template = templates[i % templates.length];
    const pass = Math.floor(i / templates.length);
    const ground = grounds[(i + pass) % grounds.length];
    let photos: PhotoUse[] = [];
    if (template.meta.photos.max > 0 && input.pool.length) {
      const hero = input.pool[photoTurn % input.pool.length];
      photoTurn++;
      const rest = input.pool.filter((p) => p.id !== hero.id);
      photos = [hero, ...rest.slice(0, Math.max(0, template.meta.photos.max - 1))];
    }
    out.push({ template, ground, accent: input.accent, photos });
  }
  return out;
}

// The photos an option may use: the hero first, then the rest of its folder.
// Loose photos at the library root share no subject, so they bring no
// siblings. Pool order is the order Kit sent (indexed and described first).
export function photoPool(photos: ImageRef[], focus: Map<string, PhotoUse>, hero?: PhotoUse): PhotoUse[] {
  const use = (r: ImageRef): PhotoUse => focus.get(r.id) ?? { id: r.id, focusX: 0.5, focusY: 0.5, zoom: 1 };
  if (!hero) return [];
  const heroRef = photos.find((p) => p.id === hero.id);
  const folder = heroRef?.folder;
  const siblings = folder ? photos.filter((p) => p.folder === folder && p.id !== hero.id).map(use) : [];
  return [hero, ...siblings];
}
