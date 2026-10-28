import { useState } from 'react';
import { api, type GlutenReduced } from '../api';
import { usePolled } from '../usePolled';

// The 86 checklist: tick a beer that has run out and the screen and the
// printed menu leave it off; untick it to bring it back. It lists the board
// as Untappd has it, so an 86'd beer is still here to untick.
//
// 86'd beers sit at the top, because "that was a mistake, put it back" is
// the other half of this job and should not mean hunting down the list.
// Each tick saves straight away, as the gluten reduced list does.

function sameBeer(a: string, b: string): boolean {
  const key = (s: string) => s.trim().replace(/\s+/g, ' ').toLowerCase();
  return key(a) === key(b);
}

export default function MenuEightySix() {
  const [data, setData] = useState<GlutenReduced | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);

  usePolled(() =>
    api
      .eightySix()
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
      setData(await api.saveEightySix(beers));
      setSaved(true);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  }

  const isOut = (t: string) => data?.beers.some((b) => sameBeer(b, t)) ?? false;
  const out = data?.taps.filter(isOut) ?? [];
  const pouring = data?.taps.filter((t) => !isOut(t)) ?? [];

  const row = (t: string, checked: boolean) => (
    <label className="check" key={t} style={{ display: 'flex' }}>
      <input type="checkbox" checked={checked} disabled={saving} onChange={() => toggle(t)} />
      {t}
    </label>
  );

  return (
    <section className="panel">
      <h2 className="panel-title">86</h2>
      <p className="card-desc">
        Tick a beer that has run out and the screen and the printed menu leave
        it off, without waiting on Untappd. Untick it to bring it back. Once
        the beer comes off Untappd too, its 86 clears on its own. Each tick
        saves straight away.
      </p>
      {!data && !err && <p className="muted">Loading…</p>}
      {data && data.taps.length === 0 && <p className="muted">Nothing on the board yet.</p>}
      {out.length > 0 && <p className="muted">86&rsquo;d, untick to bring back</p>}
      {out.map((t) => row(t, true))}
      {out.length > 0 && pouring.length > 0 && <p className="muted">Pouring</p>}
      {pouring.map((t) => row(t, false))}
      <div className="drawer-actions">
        {saving && <span className="muted">Saving…</span>}
        {saved && !saving && <span className="muted">Saved. The board follows it now.</span>}
        {err && <span className="banner banner-error">{err}</span>}
      </div>
    </section>
  );
}
