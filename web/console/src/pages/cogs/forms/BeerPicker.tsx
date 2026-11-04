import { useState } from 'react';
import type { BrewState } from '../../../api';
import { beerName } from '../context';
import { Chip, Field } from '../controls';

// A beer: the expected one up front, "Something else" for the full list,
// and "New beer" to type a name nobody has set up yet. A new name is saved
// with the entry and lands in the console's queue; it never blocks.

export interface BeerChoice {
  id?: string;
  newName?: string;
}

export default function BeerPicker({
  state,
  value,
  onChange,
  planned,
  label = 'Beer',
  exclude,
  only,
}: {
  state: BrewState;
  value: BeerChoice;
  onChange: (c: BeerChoice) => void;
  planned?: string;
  label?: string;
  exclude?: string;
  only?: string[];
}) {
  const [all, setAll] = useState(!planned);
  const [typing, setTyping] = useState(false);
  const beers = state.beers.filter(
    (b) => b.id !== exclude && (only ? only.includes(b.id) : b.active || b.id === value.id),
  );
  return (
    <Field label={label}>
      <div className="brew-chips">
        {planned && !all && (
          <Chip
            on={value.id === planned}
            onClick={() => {
              setTyping(false);
              onChange({ id: planned });
            }}
            sub="Planned"
          >
            {beerName(state, planned)}
          </Chip>
        )}
        {all &&
          beers.map((b) => (
            <Chip
              key={b.id}
              on={value.id === b.id}
              onClick={() => {
                setTyping(false);
                onChange({ id: b.id });
              }}
            >
              {b.name}
            </Chip>
          ))}
        {!all && (
          <Chip on={false} onClick={() => setAll(true)}>
            Something else
          </Chip>
        )}
        <Chip
          on={typing}
          onClick={() => {
            setTyping(true);
            onChange({ newName: value.newName ?? '' });
          }}
        >
          New beer
        </Chip>
      </div>
      {typing && (
        <input
          className="brew-input"
          style={{ marginTop: '0.6rem' }}
          placeholder="Its name"
          autoFocus
          value={value.newName ?? ''}
          onChange={(e) => onChange({ newName: e.target.value })}
        />
      )}
    </Field>
  );
}
