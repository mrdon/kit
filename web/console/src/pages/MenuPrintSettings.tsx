import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type MenuPrint, type MenuPrintConfig } from '../api';
import { useSetChatContext } from '../chatContext';
import MenuFeaturedPanel from './MenuFeaturedPanel';

// Settings for the printed menu.
//
// Lives in the Admin area, with the other set-up-once surfaces: it changes the
// prices and wording a room full of customers reads off a table, which is not
// something anyone should wander into from the page that answers "what URL do
// I paste into the screen". The endpoints are admin-only regardless; this is
// where the tile is.
//
// The tap list is the menu's own: the same Untappd pull the wall makes, with
// Kit's items, hides and 86s applied on the Menu page. Kit does not fetch
// descriptions (untappd.com refuses a server), so this page lists the beers
// that would print without one and is where they get written.
//
// Everything else here is paper-only: the masthead wording, the featured beer,
// and the headings that are a sentence rather than a list, which is how snacks
// go on a beer menu.
//
// It is one form saved whole, not a set of live-updating fields. The config is
// stored as a single document and replaced on write, so a per-field save would
// be a lie about what the backend does, and a half-typed blurb landing on a
// printed menu mid-edit is worse than an explicit Save.

// A blurb is edited as a heading and a sentence side by side, not as a map
// entry. Typing into a key rewrites it on every keystroke, which remounts the
// input and loses the cursor after the first letter; an array holds still
// while it is being typed into and is folded back into a map on save.
type BlurbRow = { name: string; text: string };

function toBlurbRows(blurbs: Record<string, string> | undefined): BlurbRow[] {
  return Object.entries(blurbs ?? {}).map(([name, text]) => ({ name, text }));
}

export default function MenuPrintSettings() {
  useSetChatContext('the printed menu settings page');
  const [data, setData] = useState<MenuPrint | null>(null);
  const [cfg, setCfg] = useState<MenuPrintConfig | null>(null);
  const [blurbs, setBlurbs] = useState<BlurbRow[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    api
      .menuPrint()
      .then((d) => {
        setData(d);
        setCfg(d.config);
        setBlurbs(toBlurbRows(d.config.blurbs));
      })
      .catch((e) => setErr(e instanceof Error ? e.message : String(e)));
  }, []);

  // set patches one field. The config is small enough that copying it whole is
  // cheaper to read than a reducer would be.
  function set<K extends keyof MenuPrintConfig>(
    key: K,
    value: MenuPrintConfig[K],
  ) {
    setCfg((c) => (c ? { ...c, [key]: value } : c));
    setSaved(false);
  }

  function setBlurb(i: number, patch: Partial<BlurbRow>) {
    setBlurbs((rows) => rows.map((r, j) => (j === i ? { ...r, ...patch } : r)));
    setSaved(false);
  }

  // setNote writes a description into config.notes, the layer that wins over
  // anything scraped. Keyed by the beer's name as the board spells it.
  function setNote(name: string, text: string) {
    if (!cfg) return;
    const notes = { ...(cfg.notes ?? {}) };
    if (text.trim()) notes[name] = text;
    else delete notes[name];
    set('notes', notes);
  }

  async function save() {
    if (!cfg) return;
    setSaving(true);
    setErr(null);
    // A half-filled blurb row is someone mid-thought, not a heading. Dropping
    // the incomplete ones here means Add can leave a blank row on screen
    // without it printing an empty bar.
    const map: Record<string, string> = {};
    for (const { name, text } of blurbs) {
      if (name.trim() && text.trim()) map[name.trim()] = text.trim();
    }
    try {
      const next = await api.saveMenuPrint({ ...cfg, blurbs: map });
      setData(next);
      setCfg(next.config);
      setBlurbs(toBlurbRows(next.config.blurbs));
      setSaved(true);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  }

  const sections = data?.sections ?? [];
  const missing = (data?.beers ?? []).filter((b) => !b.note.trim()).map((b) => b.name);

  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <Link to="/admin">Admin</Link>
          <span className="crumb-sep">/</span>
          <span>Printed menu</span>
        </nav>
        <h1>Printed menu</h1>
        <p className="page-sub">
          The beers, prices and strengths come from the menu, the same one the
          wall shows. Cans, hidden beers and 86s are set on the{' '}
          <Link to="/menu">Menu page</Link>. This is the wording around the
          edges and what each beer says.
        </p>
      </div>

      {err && <p className="banner banner-error">{err}</p>}
      {!cfg && !err && <p className="muted">Loading…</p>}

      {cfg && data && (
        <>
          <section className="panel">
            <h2 className="panel-title">Tap list</h2>
            <p className="card-desc">
              {data.synced_at ? (
                <>
                  {data.beers.length} beers, as of{' '}
                  {new Date(data.synced_at).toLocaleString()}. It follows
                  Untappd on its own, like the wall.
                </>
              ) : (
                <em>No tap list yet. It fills in from the Untappd board the wall follows.</em>
              )}
            </p>
            {missing.length > 0 && (
              <p className="banner banner-error">
                No description yet, so these print without one:{' '}
                {missing.join(', ')}. Write them below.
              </p>
            )}
          </section>

          {data.beers.length > 0 && (
            <section className="panel">
              <h2 className="panel-title">What each beer says</h2>
              <p className="card-desc">
                Style, strength and price follow the board. The description is
                yours: anything you write here is what prints.
              </p>
              {data.beers.map((b) => (
                <div className="field-row" key={b.name}>
                  <label className="field" style={{ flex: '2 1 20rem' }}>
                    <span>
                      {b.name}
                      {b.style && <> · {b.style}</>}
                      {b.abv && <> · {b.abv}</>}
                      {b.price && <> · {b.price}</>}
                      {b.written && <> · edited here</>}
                    </span>
                    <input
                      value={cfg.notes?.[b.name] ?? b.note ?? ''}
                      placeholder={
                        b.note ? b.note : 'No description yet — write one'
                      }
                      onChange={(e) => setNote(b.name, e.target.value)}
                    />
                  </label>
                </div>
              ))}
            </section>
          )}

          <MenuFeaturedPanel
            featured={cfg.featured}
            beers={data?.beers ?? []}
            onChange={(f) => set('featured', f)}
          />

          <section className="panel">
            <h2 className="panel-title">Wording</h2>
            <div className="field-row">
              <label className="field">
                <span>Masthead title</span>
                <input
                  value={cfg.title ?? ''}
                  placeholder="Beers"
                  onChange={(e) => set('title', e.target.value)}
                />
              </label>
              <label className="field">
                <span>…and after it</span>
                <input
                  value={cfg.subtitle ?? ''}
                  placeholder="& Beverages"
                  onChange={(e) => set('subtitle', e.target.value)}
                />
              </label>
            </div>
            <label className="field">
              <span>Flight badge</span>
              <input
                value={cfg.flight ?? ''}
                placeholder="Any four 4oz pours"
                onChange={(e) => set('flight', e.target.value)}
              />
              <span className="field-note">
                Printed under “Try a flight” in the masthead. Keep it short.
              </span>
            </label>
            <label className="field">
              <span>Pour sizes note</span>
              <input
                value={cfg.sizes ?? ''}
                placeholder="Pours are 16oz unless marked · Also available in 9oz & 4oz"
                onChange={(e) => set('sizes', e.target.value)}
              />
              <span className="field-note">
                The menu prints one price per beer. This explains what that
                price buys.
              </span>
            </label>
            <div className="field-row">
              <label className="field">
                <span>Bottom left</span>
                <input
                  value={cfg.foot_left ?? ''}
                  placeholder="WIFI: Gravity  PW: …"
                  onChange={(e) => set('foot_left', e.target.value)}
                />
              </label>
              <label className="field">
                <span>Bottom right</span>
                <input
                  value={cfg.foot_right ?? ''}
                  placeholder="@thegravitybrewing"
                  onChange={(e) => set('foot_right', e.target.value)}
                />
              </label>
            </div>
          </section>

          <section className="panel">
            <h2 className="panel-title">Say it in a line</h2>
            <p className="card-desc">
              A heading with a sentence under it instead of a price list. Use it
              for snacks — one line beats six rows and six prices — or to say
              something about a section that is already on the board.
            </p>
            <p className="card-desc">
              A heading that matches your tap list prints its sentence above
              those beers. One that matches nothing becomes its own section at
              the end of the menu.
            </p>
            {blurbs.map((row, i) => (
              <div className="field-row" key={i}>
                <label className="field">
                  <span>Heading</span>
                  <input
                    value={row.name}
                    placeholder="Snacks"
                    list="menu-print-sections"
                    onChange={(e) => setBlurb(i, { name: e.target.value })}
                  />
                </label>
                <label className="field">
                  <span>Sentence</span>
                  <input
                    value={row.text}
                    placeholder="Pretzels, chips and popcorn — ask at the bar."
                    onChange={(e) => setBlurb(i, { text: e.target.value })}
                  />
                </label>
                <button
                  type="button"
                  className="btn btn-ghost"
                  onClick={() => {
                    setBlurbs(blurbs.filter((_, j) => j !== i));
                    setSaved(false);
                  }}
                >
                  Remove
                </button>
              </div>
            ))}
            <div className="drawer-actions">
              <button
                type="button"
                className="btn btn-ghost"
                onClick={() => {
                  setBlurbs([...blurbs, { name: '', text: '' }]);
                  setSaved(false);
                }}
              >
                Add a heading
              </button>
            </div>
          </section>

          <datalist id="menu-print-sections">
            {sections.map((s) => (
              <option key={s} value={s} />
            ))}
          </datalist>

          <section className="panel">
            <div className="drawer-actions">
              <button className="btn" onClick={save} disabled={saving}>
                {saving ? 'Saving…' : saved ? 'Saved' : 'Save'}
              </button>
              <a
                className="btn btn-ghost"
                href={data.print_url}
                target="_blank"
                rel="noreferrer"
              >
                Open printable menu
              </a>
            </div>
            <p className="card-desc">
              The sheet is drawn from what is stored here, so save first and
              the next print has your changes. Nothing is fetched when you open
              it.
            </p>
          </section>
        </>
      )}
    </div>
  );
}
