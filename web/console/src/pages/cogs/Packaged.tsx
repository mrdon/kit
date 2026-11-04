import { useSetChatContext } from '../../chatContext';
import type { BrewFormat } from '../../api';
import { bbl, shortDate, useBrew } from './context';

// Packaged: the chiller's kegs, cans and bottles as the system calculates
// them, with the oldest batch's age. The count, repacks and "out, not sold"
// live here because they act on packaged beer, not on a tank.

function fmtQty(units: number, f: BrewFormat | undefined): string {
  if (!f || f.kind === 'keg' || f.units_per_case <= 0) return String(Number(units.toFixed(2)));
  const sign = units < 0 ? '−' : '';
  const abs = Math.abs(units);
  const cases = Math.floor(abs / f.units_per_case);
  const loose = Math.round(abs - cases * f.units_per_case);
  return sign + (loose ? `${cases} cs + ${loose}` : `${cases} cs`);
}

export default function Packaged() {
  useSetChatContext('the brewing packaged-beer page');
  const { state, open } = useBrew();
  const fmt = (id: string) => state.formats.find((f) => f.id === id);
  const lastCount = state.last_count ? shortDate(state.last_count) : 'never';
  return (
    <div>
      <div className="brew-toolbar">
        <button type="button" className="brew-btn brew-btn-primary brew-btn-big" onClick={() => open({ kind: 'count' })}>
          Count
        </button>
        <button type="button" className="brew-btn brew-btn-big" onClick={() => open({ kind: 'repack' })}>
          Repack or make a variant
        </button>
        <button type="button" className="brew-btn brew-btn-big" onClick={() => open({ kind: 'out' })}>
          Out, not sold
        </button>
      </div>
      <p className="brew-hint" style={{ marginBottom: '0.75rem' }}>
        Last counted {lastCount}. Packaged minus what's gone out; the count corrects it.
      </p>
      {state.packaged.length === 0 ? (
        <p className="brew-empty">No packaged beer on record.</p>
      ) : (
        <table className="brew-table">
          <thead>
            <tr>
              <th>Beer</th>
              <th>Format</th>
              <th className="num">On hand</th>
              <th className="num">bbl</th>
              <th>Oldest</th>
            </tr>
          </thead>
          <tbody>
            {state.packaged.map((r) => (
              <tr key={`${r.beer_id}:${r.format_id}`}>
                <td>{r.beer}</td>
                <td>{r.format}</td>
                <td className="num">{fmtQty(r.units, fmt(r.format_id))}</td>
                <td className="num">{bbl(r.bbl)}</td>
                <td>
                  {r.oldest_on ? `${Math.floor(r.age_weeks)} wk` : '–'}
                  {r.past_shelf && <span className="brew-stale"> · past shelf life</span>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
