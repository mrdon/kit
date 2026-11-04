import { useEffect, useState } from 'react';
import { api, type BrewTile, type HistoryEntry } from '../../api';
import { bbl, shortDate, tileById, useBrew, type FormSpec } from './context';
import { Sheet } from './controls';

// The tank sheet: what's in it, what can be done to it, and its last five
// entries. Only the newest entry has Undo here; an older mistake is the
// console's job.

interface Act {
  label: string;
  form: FormSpec;
}

function actionsFor(t: BrewTile): Act[] {
  const id = t.id;
  const has = !!t.batch_id;
  const spill: Act = { label: 'Spilled or dumped', form: { kind: 'spill', vesselId: id } };
  const level: Act = { label: 'Read the level', form: { kind: 'level', vesselId: id } };
  if (!has) {
    const out: Act[] =
      t.kind === 'fermenter'
        ? [{ label: 'Brewed into it', form: { kind: 'brew', vesselId: id } }]
        : [{ label: 'Filled it', form: { kind: 'move', toVesselId: id } }];
    if (t.dirty) out.push({ label: 'Cleaned it', form: { kind: 'confirm', action: 'clean', vesselId: id } });
    return out;
  }
  switch (t.kind) {
    case 'fermenter':
      return [
        { label: 'Dry hopped', form: { kind: 'confirm', action: 'dry_hop', vesselId: id } },
        { label: 'Moved it', form: { kind: 'move', vesselId: id } },
        { label: 'Packaged it', form: { kind: 'package', vesselId: id } },
        spill,
        { label: 'Became a variant', form: { kind: 'variant', vesselId: id } },
      ];
    case 'brite':
      return [
        { label: 'Packaged it', form: { kind: 'package', vesselId: id } },
        { label: 'Moved to a serving tank', form: { kind: 'move', vesselId: id } },
        level,
        spill,
      ];
    case 'serving':
      return [
        t.on_tap
          ? { label: 'It kicked', form: { kind: 'confirm', action: 'kick', vesselId: id } }
          : { label: 'On tap now', form: { kind: 'confirm', action: 'on_tap', vesselId: id } },
        { label: 'Kegged it', form: { kind: 'package', vesselId: id } },
        level,
        spill,
      ];
    case 'barrel':
      return [
        { label: 'Pulled it', form: { kind: 'move', vesselId: id } },
        { label: 'Packaged it', form: { kind: 'package', vesselId: id } },
        spill,
      ];
  }
  return [];
}

export default function TankDetail({ vesselId }: { vesselId: string }) {
  const { state, open, close, undo } = useBrew();
  const tile = tileById(state, vesselId);
  const [history, setHistory] = useState<HistoryEntry[] | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    api
      .brewHistory(vesselId, 5)
      .then((r) => setHistory(r.entries))
      .catch((e) => setErr((e as Error).message));
  }, [vesselId, state]);

  if (!tile) return null;
  const sub = tile.beer
    ? `${tile.beer} ${tile.batch_code} · ${tile.stage_label} · ${tile.age_label} · ${bbl(tile.volume_bbl)} of ${tile.size_bbl} bbl`
    : `${tile.stage_label} · ${tile.size_bbl} bbl`;

  return (
    <Sheet title={tile.name} sub={sub} onClose={close}>
      {tile.next && <p className="brew-hint">{tile.next}</p>}
      <div className="brew-section brew-actions">
        {actionsFor(tile).map((a) => (
          <button key={a.label} type="button" className="brew-btn" onClick={() => open(a.form)}>
            {a.label}
          </button>
        ))}
      </div>
      <div className="brew-section">
        <span className="brew-label">History</span>
        {err && <p className="brew-error">{err}</p>}
        {history && history.length === 0 && <p className="brew-empty">Nothing logged yet.</p>}
        <ul className="brew-history">
          {history?.map((h) => (
            <li key={h.action_id} className={h.reversed ? 'brew-history-reversed' : ''}>
              <span className="brew-history-date">{shortDate(h.at)}</span>
              <span className="brew-history-text">{h.text}</span>
              {h.can_undo && !h.reversed && (
                <button type="button" className="brew-btn" onClick={() => void undo(h.action_id)}>
                  Undo
                </button>
              )}
            </li>
          ))}
        </ul>
        <p className="brew-hint">Got an older one wrong? An admin can fix it from the console.</p>
      </div>
    </Sheet>
  );
}
