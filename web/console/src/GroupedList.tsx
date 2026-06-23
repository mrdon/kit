import { useEffect, useState } from 'react';

// A group of list rows under one label (e.g. a role, or "Everyone").
export interface Group<T> {
  key: string;
  label: string;
  items: T[];
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
