import { useState } from 'react';
import { api, type GlutenReduced } from '../api';
import { usePolled } from '../usePolled';

// The gluten reduced checklist, shown as a panel on the Menu page because it
// marks both the screen and the printed menu.
//
// It saves each tick as it is made. A dietary mark that looks set but was
// never saved -- ticked, then the tab closed before a Save button -- is the
// one mistake here a customer pays for.
//
// Beers that are marked but not on the board are listed too, because the mark
// is kept by beer name and outlives the tap: that is how a seasonal comes back
// marked. Without them there would be no way to take a retired beer off.

function sameBeer(a: string, b: string): boolean {
  const key = (s: string) => s.trim().replace(/\s+/g, ' ').toLowerCase();
  return key(a) === key(b);
}

export default function MenuGlutenReduced() {
  const [data, setData] = useState<GlutenReduced | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);

  // Polled so a tick made on the other iPad, or by an agent, shows here;
  // a save in flight wins over a poll that raced it.
  usePolled(() =>
    api
      .glutenReduced()
      .then((d) => {
        if (!saving) setData(d);
      })
      .catch((e) => setErr(e instanceof Error ? e.message : String(e))),
  );

  async function toggle(name: string) {
    if (!data || saving) return;
    const on = data.beers.some((b) => sameBeer(b, name));
    const beers = on ? data.beers.filter((b) => !sameBeer(b, name)) : [...data.beers, name];
    setSaving(true);
    setSaved(false);
    setErr(null);
    try {
      setData(await api.saveGlutenReduced(beers));
      setSaved(true);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  }

  const offBoard = (data?.beers ?? []).filter((b) => !data?.taps.some((t) => sameBeer(t, b)));

  return (
    <section className="panel">
      <h2 className="panel-title">Gluten reduced</h2>
      <p className="card-desc">
        Tick the beers brewed to reduce gluten. The screen shows a GR badge
        beside each one with &ldquo;Gluten reduced. May contain gluten.&rdquo;
        in the footer, and the printed menu puts the same line in the
        beer&rsquo;s description. A beer keeps its mark when it goes off
        tap, so a seasonal comes back marked. Each tick saves straight away.
      </p>
      {!data && !err && <p className="muted">Loading…</p>}
      {data && data.taps.length === 0 && offBoard.length === 0 && (
        <p className="muted">Nothing on the board yet.</p>
      )}
      {data?.taps.map((t) => (
        <label className="check" key={t} style={{ display: 'flex' }}>
          <input
            type="checkbox"
            checked={data.beers.some((b) => sameBeer(b, t))}
            disabled={saving}
            onChange={() => toggle(t)}
          />
          {t}
        </label>
      ))}
      {offBoard.map((b) => (
        <label className="check" key={b} style={{ display: 'flex' }}>
          <input type="checkbox" checked disabled={saving} onChange={() => toggle(b)} />
          {b} · not on tap right now
        </label>
      ))}
      <div className="drawer-actions">
        {saving && <span className="muted">Saving…</span>}
        {saved && !saving && <span className="muted">Saved. The board follows it now.</span>}
        {err && <span className="banner banner-error">{err}</span>}
      </div>
    </section>
  );
}
