import { useEffect, useState } from 'react';

// A group of list rows under one label (e.g. a role, or "Everyone").
export interface Group<T> {
  key: string;
  label: string;
  items: T[];
}

// Display order for scope tiers, broadest to narrowest. public/everyone share
// the top slot (they're the broadest audience on their respective surfaces).
const SCOPE_ORDER: Record<string, number> = {
  public: 0,
  everyone: 0,
  members: 1,
  role: 2,
  personal: 3,
  builtin: 4,
};

// groupByScope buckets any scoped items by the backend's scope_kind/scope_label
// — the single source of truth (services.ScopeTierOf). The frontend never
// re-derives tiers, so Skills and Jobs group identically and can't drift.
export function groupByScope<T extends { scope_kind: string; scope_label: string }>(
  items: T[],
): Group<T>[] {
  const buckets = new Map<string, { kind: string; label: string; items: T[] }>();
  for (const it of items) {
    const key = it.scope_kind === 'role' ? `role:${it.scope_label}` : it.scope_kind;
    const b = buckets.get(key) ?? { kind: it.scope_kind, label: it.scope_label, items: [] };
    b.items.push(it);
    buckets.set(key, b);
  }
  return [...buckets.entries()]
    .sort(
      ([, a], [, b]) =>
        (SCOPE_ORDER[a.kind] ?? 9) - (SCOPE_ORDER[b.kind] ?? 9) ||
        a.label.localeCompare(b.label),
    )
    .map(([key, b]) => ({ key, label: b.label, items: b.items }));
}

// GroupedList renders filter chips (All + one per non-empty group, each with a
// count) over a list that's either split into titled sections (when "All" is
// selected and there's more than one group) or flattened to a single group.
// renderItem must return a keyed <li>. Shared by the Skills and Jobs pages so
// the two surfaces stay consistent.
export default function GroupedList<T>({
  groups,
  renderItem,
  empty,
}: {
  groups: Group<T>[];
  renderItem: (item: T) => React.ReactNode;
  empty?: string;
}) {
  const nonEmpty = groups.filter((g) => g.items.length > 0);
  const [active, setActive] = useState<string | null>(null);

  // Drop the active filter if its group emptied out (e.g. after a delete).
  useEffect(() => {
    if (active && !nonEmpty.some((g) => g.key === active)) setActive(null);
  }, [active, nonEmpty]);

  const total = nonEmpty.reduce((n, g) => n + g.items.length, 0);
  if (total === 0) return <p className="muted">{empty ?? 'Nothing here yet.'}</p>;

  const shown = active ? nonEmpty.filter((g) => g.key === active) : nonEmpty;
  const showHeaders = !active && nonEmpty.length > 1;

  return (
    <>
      <div className="filter-chips">
        <button
          className={`chip${active === null ? ' chip-active' : ''}`}
          onClick={() => setActive(null)}
        >
          All <span className="chip-count">{total}</span>
        </button>
        {nonEmpty.map((g) => (
          <button
            key={g.key}
            className={`chip${active === g.key ? ' chip-active' : ''}`}
            onClick={() => setActive(g.key)}
          >
            {g.label} <span className="chip-count">{g.items.length}</span>
          </button>
        ))}
      </div>
      {shown.map((g) => (
        <section key={g.key} className="group">
          {showHeaders && <h3 className="group-head">{g.label}</h3>}
          <ul className="entry-list">{g.items.map(renderItem)}</ul>
        </section>
      ))}
    </>
  );
}
