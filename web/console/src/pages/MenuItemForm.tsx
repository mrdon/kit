import { useState } from 'react';
import type { MenuItem } from '../api';

// One Kit item, added or edited. Price and size live in the item's first
// pour, the shape both menus read; the label tracks the size because it is
// what marks the row as a can rather than a pour.

export const BLANK_ITEM: MenuItem = {
  section: '',
  name: '',
  style: '',
  pours: [{ size: '12oz', label: '12oz Can', price: '' }],
};

export default function MenuItemForm({
  initial,
  sections,
  busy,
  onSave,
  onCancel,
}: {
  initial: MenuItem;
  sections: string[];
  busy: boolean;
  onSave: (item: MenuItem) => void;
  onCancel: () => void;
}) {
  const [item, setItem] = useState<MenuItem>(initial);
  const pour = item.pours?.[0] ?? BLANK_ITEM.pours![0];
  const setPour = (patch: { size?: string; price?: string }) => {
    const next = { ...pour, ...patch };
    if (patch.size !== undefined) next.label = `${patch.size} Can`;
    setItem({ ...item, pours: [next] });
  };
  const ready = item.name.trim() !== '' && item.section.trim() !== '' && pour.price.trim() !== '';

  return (
    <div className="field-row">
      <label className="field">
        <span>Section</span>
        <input
          value={item.section}
          placeholder="Non Alcoholic"
          list="menu-item-sections"
          onChange={(e) => setItem({ ...item, section: e.target.value })}
        />
      </label>
      <label className="field">
        <span>Name</span>
        <input
          value={item.name}
          placeholder="Athletic Run Wild"
          onChange={(e) => setItem({ ...item, name: e.target.value })}
        />
      </label>
      <label className="field">
        <span>Style</span>
        <input
          value={item.style ?? ''}
          placeholder="optional"
          onChange={(e) => setItem({ ...item, style: e.target.value })}
        />
      </label>
      <label className="field">
        <span>Size</span>
        <input value={pour.size} placeholder="12oz" onChange={(e) => setPour({ size: e.target.value })} />
      </label>
      <label className="field">
        <span>Price</span>
        <input value={pour.price} placeholder="6" onChange={(e) => setPour({ price: e.target.value })} />
      </label>
      <label className="field">
        <span>Show on</span>
        <select
          value={item.show || 'both'}
          onChange={(e) => setItem({ ...item, show: e.target.value as MenuItem['show'] })}
        >
          <option value="both">Both</option>
          <option value="board">Wall</option>
          <option value="print">Paper</option>
        </select>
      </label>
      <datalist id="menu-item-sections">
        {sections.map((s) => (
          <option key={s} value={s} />
        ))}
      </datalist>
      <div className="row-actions">
        <button className="btn btn-sm" type="button" disabled={!ready || busy} onClick={() => onSave(item)}>
          Save
        </button>
        <button className="btn btn-ghost btn-sm" type="button" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </div>
  );
}
