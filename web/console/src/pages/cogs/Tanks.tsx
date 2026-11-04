import { useSetChatContext } from '../../chatContext';
import type { BrewTile, WeekItem } from '../../api';
import { bbl, useBrew, type FormSpec } from './context';

// Tanks is the brewhouse at a glance: a tile per vessel, grouped the way
// the building is (brewhouse, chiller, barrels), and This week down the
// side with a button on anything that needs doing.

const AREAS: { key: string; title: string }[] = [
  { key: 'brewhouse', title: 'Brewhouse' },
  { key: 'chiller', title: 'Chiller' },
  { key: 'barrels', title: 'Barrels' },
];

const BADGE_TEXT: Record<string, string> = {
  due: 'Due',
  dirty: 'Dirty',
  empty: 'Empty',
  on_tap: 'On tap',
};

export default function Tanks() {
  useSetChatContext('the brewing tanks page');
  const { state, open } = useBrew();
  if (state.vessels.length === 0) {
    return (
      <p className="brew-empty">
        No tanks are set up yet. An admin adds them from COGS setup on the console.
      </p>
    );
  }
  return (
    <div className="brew-home">
      <div>
        {AREAS.map((a) => {
          const tiles = state.vessels.filter((v) => v.area === a.key);
          if (tiles.length === 0) return null;
          return (
            <section key={a.key}>
              <h2 className="brew-area-title">{a.title}</h2>
              <div className="brew-tiles">
                {tiles.map((t) => (
                  <Tile key={t.id} tile={t} onOpen={() => open({ kind: 'tank', vesselId: t.id })} />
                ))}
              </div>
            </section>
          );
        })}
      </div>
      <ThisWeek items={state.this_week} />
    </div>
  );
}

function Tile({ tile, onOpen }: { tile: BrewTile; onOpen: () => void }) {
  const pct = tile.size_bbl > 0 ? Math.min(100, (tile.volume_bbl / tile.size_bbl) * 100) : 0;
  return (
    <button type="button" className="brew-tile" onClick={onOpen}>
      <span className="brew-tile-head">
        <span className="brew-tile-name">{tile.name}</span>
        <span className="brew-tile-size">{tile.size_bbl} bbl</span>
      </span>
      {tile.beer ? (
        <>
          <span className="brew-tile-beer">{tile.beer}</span>
          <span className="brew-tile-stage">
            {tile.stage_label} · {tile.age_label}
          </span>
          <span className="brew-fill" aria-label={`${bbl(tile.volume_bbl)} of ${tile.size_bbl} bbl`}>
            <span style={{ width: `${pct}%` }} />
          </span>
        </>
      ) : null}
      {tile.badge && <span className={`brew-badge brew-badge-${tile.badge}`}>{BADGE_TEXT[tile.badge]}</span>}
      <span className="brew-tile-next">{tile.next}</span>
    </button>
  );
}

// weekForm is the form a This-week item opens, pre-filled.
function weekForm(it: WeekItem): FormSpec | null {
  switch (it.action) {
    case 'brew':
      return { kind: 'brew', vesselId: it.vessel_id, planEntryId: it.plan_entry_id };
    case 'count':
      return { kind: 'count' };
    case 'clean':
    case 'dry_hop':
      return it.vessel_id ? { kind: 'confirm', action: it.action, vesselId: it.vessel_id } : null;
    case 'package':
      return it.vessel_id ? { kind: 'package', vesselId: it.vessel_id } : null;
    case 'transfer':
      return it.vessel_id ? { kind: 'move', vesselId: it.vessel_id } : null;
  }
  return it.vessel_id ? { kind: 'tank', vesselId: it.vessel_id } : null;
}

const ACTION_LABEL: Record<string, string> = {
  brew: 'Brew day',
  count: 'Count',
  clean: 'Cleaned it',
  dry_hop: 'Dry hopped',
  package: 'Package it',
  transfer: 'Move it',
};

function ThisWeek({ items }: { items: WeekItem[] }) {
  const { open } = useBrew();
  return (
    <aside className="brew-week">
      <h2>This week</h2>
      {items.length === 0 ? (
        <p className="brew-empty">Nothing due.</p>
      ) : (
        <ul>
          {items.map((it, i) => {
            const f = weekForm(it);
            return (
              <li key={i}>
                <span className="brew-week-text">
                  {it.text}
                  {it.when && <span className="brew-week-when">{it.when}</span>}
                </span>
                {f && (
                  <button
                    type="button"
                    className={`brew-btn ${it.due ? 'brew-btn-primary' : ''}`}
                    onClick={() => open(f)}
                  >
                    {ACTION_LABEL[it.action ?? ''] ?? 'Open'}
                  </button>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </aside>
  );
}
