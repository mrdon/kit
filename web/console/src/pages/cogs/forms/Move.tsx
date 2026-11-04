import { useState } from 'react';
import type { BrewTile } from '../../../api';
import { bbl, tileById, useBrew } from '../context';
import { Check, Chip, Field, SaveBar, Sheet, Stepper, WhenChips, useSave } from '../controls';

// Moved it / Filled it / Pulled it: beer from one vessel to another. From a
// fermenter it can also go into barrels. The "is now empty" box ticks
// itself when the move takes about the whole tank; the brewer can override.

const NEAR_EMPTY_BBL = 0.3;

type DestState = { ok: boolean; note: string };

function destState(src: BrewTile, d: BrewTile): DestState {
  const room = Math.max(0, d.size_bbl - d.volume_bbl);
  if (!d.batch_id) return { ok: true, note: d.dirty ? 'Dirty: logs a clean too' : `Empty · ${d.size_bbl} bbl` };
  if (d.beer_id !== src.beer_id) return { ok: false, note: d.beer };
  if (room < 0.1) return { ok: false, note: 'Full' };
  return { ok: true, note: `Add to its ${bbl(d.volume_bbl)} bbl` };
}

export default function Move({ vesselId, toVesselId }: { vesselId?: string; toVesselId?: string }) {
  const { state, close } = useBrew();
  const [srcId, setSrcId] = useState(vesselId);
  const [dstId, setDstId] = useState(toVesselId);
  const [barrels, setBarrels] = useState(0);
  const src = tileById(state, srcId);
  const dst = tileById(state, dstId);
  const pulling = src?.kind === 'barrel';
  const room = dst ? Math.max(0, dst.size_bbl - dst.volume_bbl) : Infinity;
  const [volume, setVolume] = useState(() => (pulling ? 0 : Math.min(src?.volume_bbl ?? 0, room)));
  const [emptyTouched, setEmptyTouched] = useState(false);
  const [emptyBox, setEmptyBox] = useState(false);
  const [date, setDate] = useState('');
  // Usually what left is what arrived; a measured shortfall is a loss.
  const [lossOnWay, setLossOnWay] = useState(false);
  const [arrived, setArrived] = useState(0);
  const { saving, error, save } = useSave();

  const emptyBarrels = state.vessels.filter((v) => v.kind === 'barrel' && !v.batch_id);
  const barrelBBL = state.settings.barrel_bbl;
  const toBarrels = dstId === 'barrels';
  const moved = toBarrels ? barrels * barrelBBL : volume;
  const autoEmpty = !!src && (pulling || src.volume_bbl - moved <= NEAR_EMPTY_BBL);
  const empty = emptyTouched ? emptyBox : autoEmpty;

  const sources = state.vessels.filter((v) => v.batch_id && v.id !== toVesselId);
  const dests = state.vessels.filter((v) => v.id !== srcId && v.kind !== 'barrel');

  const pickSource = (id: string) => {
    setSrcId(id);
    const s = tileById(state, id);
    setVolume(s?.kind === 'barrel' ? 0 : Math.min(s?.volume_bbl ?? 0, room));
    setEmptyTouched(false);
  };
  const pickDest = (id: string) => {
    setDstId(id);
    const d = tileById(state, id);
    if (d && src && !pulling) setVolume(Math.min(src.volume_bbl, Math.max(0, d.size_bbl - d.volume_bbl)));
    setEmptyTouched(false);
  };

  const submit = () => {
    if (toBarrels) {
      return save({
        kind: 'barrel_fill',
        date,
        vessel_id: srcId,
        to_vessel_ids: emptyBarrels.slice(0, barrels).map((b) => b.id),
        empty,
      });
    }
    return save({
      kind: 'transfer',
      date,
      vessel_id: srcId,
      to_vessel_id: dstId,
      volume_bbl: pulling ? src?.volume_bbl : volume,
      volume_in_bbl: !pulling && lossOnWay ? arrived : volume,
      empty,
    });
  };

  const title = toVesselId ? `Fill ${dst?.name ?? ''}` : pulling ? `Pull ${src?.name ?? ''}` : `Move ${src?.name ?? ''}`;
  const sub = src?.beer ? `${src.beer} · ${bbl(src.volume_bbl)} bbl in ${src.name}` : undefined;

  return (
    <Sheet title={title} sub={sub} onClose={close}>
      {toVesselId && (
        <Field label="From">
          <div className="brew-chips">
            {sources.map((s) => (
              <Chip key={s.id} on={srcId === s.id} onClick={() => pickSource(s.id)} sub={`${s.beer} · ${bbl(s.volume_bbl)} bbl`}>
                {s.name}
              </Chip>
            ))}
          </div>
        </Field>
      )}
      {!toVesselId && src && (
        <Field label="To">
          <div className="brew-chips">
            {dests.map((d) => {
              const st = destState(src, d);
              return (
                <Chip key={d.id} on={dstId === d.id} disabled={!st.ok} onClick={() => pickDest(d.id)} sub={st.note}>
                  {d.name}
                </Chip>
              );
            })}
            {src.kind === 'fermenter' && emptyBarrels.length > 0 && (
              <Chip on={toBarrels} onClick={() => setDstId('barrels')} sub={`${emptyBarrels.length} empty`}>
                Barrels
              </Chip>
            )}
          </div>
        </Field>
      )}
      {toBarrels ? (
        <Field label="Barrels filled" hint={`${bbl(moved)} bbl at ${barrelBBL} bbl a barrel`}>
          <Stepper
            label="Barrels"
            value={barrels}
            max={Math.min(emptyBarrels.length, Math.floor((src?.volume_bbl ?? 0) / barrelBBL + 0.05))}
            onChange={setBarrels}
          />
        </Field>
      ) : (
        <Field
          label={pulling ? 'What came out' : 'How much'}
          hint={pulling ? 'Read it in the tank it went into. The rest is written off as evaporation.' : undefined}
        >
          <Stepper
            label="Volume"
            value={volume}
            step={0.1}
            big={1}
            decimals={1}
            unit="bbl"
            max={Number.isFinite(room) ? room : undefined}
            onChange={setVolume}
          />
        </Field>
      )}
      {!toBarrels && !pulling && (
        <div className="brew-section">
          <Check
            checked={lossOnWay}
            onChange={(v) => {
              setLossOnWay(v);
              setArrived(volume);
            }}
          >
            Some was lost on the way
          </Check>
          {lossOnWay && (
            <Field label={`What arrived in ${dst?.name ?? 'the tank'}`} hint={`${bbl(Math.max(0, volume - arrived))} bbl written off`}>
              <Stepper label="Arrived" value={arrived} step={0.1} big={1} decimals={1} unit="bbl" max={volume} onChange={setArrived} />
            </Field>
          )}
        </div>
      )}
      {src && (
        <div className="brew-section">
          <Check
            checked={empty}
            onChange={(v) => {
              setEmptyTouched(true);
              setEmptyBox(v);
            }}
          >
            {src.name} is now empty
          </Check>
          <p className="brew-hint">
            {empty
              ? `Whatever's left is written off and ${src.name} is marked dirty.`
              : `${bbl(Math.max(0, src.volume_bbl - moved))} bbl stays in ${src.name}.`}
          </p>
        </div>
      )}
      <WhenChips today={state.today} value={date} onChange={setDate} />
      <SaveBar
        onCancel={close}
        onSave={() => void submit()}
        saving={saving}
        disabled={!src || !dstId || (toBarrels ? barrels < 1 : volume <= 0)}
        error={error}
      />
    </Sheet>
  );
}
