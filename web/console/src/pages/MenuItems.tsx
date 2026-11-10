import { useState } from 'react';
import { api, type MenuItem, type MenuItemRow, type MenuItems as Items, type MenuItemsChange } from '../api';
import { usePolled } from '../usePolled';
import MenuItemForm, { BLANK_ITEM } from './MenuItemForm';

// The menu as both the wall and the paper read it: what Untappd sends, the
// items Kit adds, and what is hidden or 86'd, in one list.
//
// Anyone can tick an 86, because whoever finds the keg blown should say so.
// Adding, editing and hiding are for admins, and take the same arguments as
// set_menu_items. Every change saves straight away.

const SHOW_LABEL: Record<string, string> = { both: 'Wall + paper', board: 'Wall only', print: 'Paper only' };

export default function MenuItems({ isAdmin }: { isAdmin: boolean }) {
  const [data, setData] = useState<Items | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // -1 adds a new item; a number edits that Kit item; null is no form open.
  const [editing, setEditing] = useState<number | null>(null);

  usePolled(() =>
    api
      .menuItems()
      .then((d) => {
        if (!busy && editing === null) setData(d);
      })
      .catch((e) => setErr(e instanceof Error ? e.message : String(e))),
  );

  async function run(op: () => Promise<Items>) {
    setBusy(true);
    setErr(null);
    try {
      setData(await op());
      setEditing(null);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  const change = (c: MenuItemsChange) => run(() => api.saveMenuItems(c));

  // The 86 list is saved whole, so the tick rebuilds it from every row.
  function toggleOut(row: MenuItemRow) {
    if (!data) return;
    const out = data.sections.flatMap((s) => s.items.filter((i) => i.out).map((i) => i.name));
    const next = row.out ? out.filter((n) => n !== row.name) : [...out, row.name];
    void run(async () => {
      await api.saveEightySix(next);
      return api.menuItems();
    });
  }

  function saveItem(item: MenuItem) {
    if (!data) return;
    const items = [...data.edits.items];
    if (editing === null || editing < 0) items.push(item);
    else items[editing] = item;
    void change({ items });
  }

  function removeItem(index: number) {
    if (!data) return;
    void change({ items: data.edits.items.filter((_, i) => i !== index) });
  }

  const sections = data?.sections.map((s) => s.name) ?? [];

  const rowView = (row: MenuItemRow) => (
    <div className="field-row" key={`${row.source}:${row.name}`} style={{ opacity: row.hidden ? 0.5 : 1 }}>
      <label className="check" style={{ display: 'flex', flex: '2 1 14rem' }}>
        <input type="checkbox" checked={row.out} disabled={busy} onChange={() => toggleOut(row)} />
        <span>
          {row.name}
          {row.source === 'kit' && <span className="tag tag-soft"> Kit · {SHOW_LABEL[row.show || 'both']}</span>}
          {row.hidden && <span className="tag tag-soft"> hidden</span>}
        </span>
      </label>
      <span className="muted" style={{ flex: '1 1 8rem' }}>
        {[row.size, row.price && `$${row.price}`].filter(Boolean).join(' ')}
      </span>
      {isAdmin && (
        <div className="row-actions">
          {row.source === 'kit' && (
            <>
              <button className="btn btn-ghost btn-sm" disabled={busy} onClick={() => setEditing(row.index)}>
                Edit
              </button>
              <button className="btn btn-ghost btn-sm" disabled={busy} onClick={() => removeItem(row.index)}>
                Remove
              </button>
            </>
          )}
          {row.source === 'untappd' && (
            <button
              className="btn btn-ghost btn-sm"
              disabled={busy}
              onClick={() => void change(row.hidden ? { unhide: [row.name] } : { hide: [row.name] })}
            >
              {row.hidden ? 'Unhide' : 'Hide'}
            </button>
          )}
        </div>
      )}
    </div>
  );

  return (
    <section className="panel">
      <h2 className="panel-title">Menu items</h2>
      <p className="card-desc">
        What the wall and the printed menu show: Untappd&rsquo;s tap list plus
        anything added here. Tick a beer that has run out to 86 it from both
        until it is back; once Untappd drops it, the 86 clears on its own.
        {isAdmin && ' Hide keeps a beer or a whole section off both menus until you unhide it.'}
      </p>
      {!data && !err && <p className="muted">Loading…</p>}
      {data?.sections.map((s) => (
        <div key={s.name} style={{ opacity: s.hidden ? 0.5 : 1 }}>
          <div className="field-row" style={{ alignItems: 'baseline' }}>
            <h3 style={{ flex: '1 1 auto', margin: '0.75rem 0 0.25rem' }}>
              {s.name}
              {s.hidden && <span className="tag tag-soft"> hidden</span>}
            </h3>
            {isAdmin && (
              <button
                className="btn btn-ghost btn-sm"
                disabled={busy}
                onClick={() => void change(s.hidden ? { unhide: [s.name] } : { hide: [s.name] })}
              >
                {s.hidden ? 'Unhide section' : 'Hide section'}
              </button>
            )}
          </div>
          {s.items.map((row) =>
            editing !== null && row.source === 'kit' && row.index === editing ? (
              <MenuItemForm
                key={`edit:${row.index}`}
                initial={data.edits.items[row.index]}
                sections={sections}
                busy={busy}
                onSave={saveItem}
                onCancel={() => setEditing(null)}
              />
            ) : (
              rowView(row)
            ),
          )}
        </div>
      ))}
      {isAdmin && editing === -1 && (
        <MenuItemForm
          initial={{ ...BLANK_ITEM, pours: [{ ...BLANK_ITEM.pours![0] }] }}
          sections={sections}
          busy={busy}
          onSave={saveItem}
          onCancel={() => setEditing(null)}
        />
      )}
      <div className="drawer-actions">
        {isAdmin && editing === null && (
          <button className="btn btn-ghost" disabled={!data || busy} onClick={() => setEditing(-1)}>
            Add an item
          </button>
        )}
        {busy && <span className="muted">Saving…</span>}
        {err && <span className="banner banner-error">{err}</span>}
      </div>
    </section>
  );
}
