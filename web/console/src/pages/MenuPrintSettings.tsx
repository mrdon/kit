import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  api,
  type MenuExtra,
  type MenuPrint,
  type MenuPrintConfig,
} from '../api';
import { useSetChatContext } from '../chatContext';

// Settings for the printed menu.
//
// Lives in the Admin area, with the other set-up-once surfaces: it changes the
// prices and wording a room full of customers reads off a table, which is not
// something anyone should wander into from the page that answers "what URL do
// I paste into the screen". The endpoints are admin-only regardless; this is
// where the tile is.
//
// Everything here is what Untappd cannot tell us. The beers, prices, strengths
// and descriptions all come from the tap list; this page is the masthead
// wording, the colour of each section bar, the rows that are not on tap at all
// — cans, sodas, juice boxes — and the headings that are a sentence rather
// than a list, which is how snacks go on a beer menu.
//
// It is one form saved whole, not a set of live-updating fields. The config is
// stored as a single document and replaced on write, so a per-field save would
// be a lie about what the backend does, and a half-typed section colour
// landing on a printed menu mid-edit is worse than an explicit Save.

// Empty rows to append, kept out of the component so the shapes are stated
// once rather than inline at each call site.
const BLANK_EXTRA: MenuExtra = {
  section: '',
  name: '',
  style: '',
  pours: [{ size: '12oz', label: '12oz Can', price: '' }],
};

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

  function setColor(section: string, hex: string) {
    if (!cfg) return;
    const colors = { ...(cfg.colors ?? {}) };
    if (hex) colors[section] = hex;
    else delete colors[section];
    set('colors', colors);
  }

  function setBlurb(i: number, patch: Partial<BlurbRow>) {
    setBlurbs((rows) => rows.map((r, j) => (j === i ? { ...r, ...patch } : r)));
    setSaved(false);
  }

  function setExtra(i: number, patch: Partial<MenuExtra>) {
    if (!cfg) return;
    const extras = [...(cfg.extras ?? [])];
    extras[i] = { ...extras[i], ...patch };
    set('extras', extras);
  }

  // Price lives inside the row's first pour, because that is the shape the
  // renderer reads. The form flattens it so nobody has to think about pours to
  // put a lemonade on the menu.
  function setExtraPrice(i: number, price: string) {
    if (!cfg) return;
    const extras = [...(cfg.extras ?? [])];
    const pours = [...(extras[i].pours ?? [{ ...BLANK_EXTRA.pours![0] }])];
    pours[0] = { ...pours[0], price };
    extras[i] = { ...extras[i], pours };
    set('extras', extras);
  }

  function setExtraSize(i: number, size: string) {
    if (!cfg) return;
    const extras = [...(cfg.extras ?? [])];
    const pours = [...(extras[i].pours ?? [{ ...BLANK_EXTRA.pours![0] }])];
    // The label is what marks a row as packaged rather than poured, so it has
    // to track the size or a can starts printing in the draft columns.
    pours[0] = { ...pours[0], size, label: `${size} Can` };
    extras[i] = { ...extras[i], pours };
    set('extras', extras);
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
  const extras = cfg?.extras ?? [];

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
          The beers, prices and strengths come from your tap list. This is
          everything else — the wording around the edges, the colour of each
          section, and the drinks that are not on tap.
        </p>
      </div>

      {err && <p className="banner banner-error">{err}</p>}
      {!cfg && !err && <p className="muted">Loading…</p>}

      {cfg && data && (
        <>
          <section className="panel">
            <h2 className="panel-title">Descriptions</h2>
            <label className="field">
              <span>Untappd brewery page</span>
              <input
                value={cfg.brand ?? ''}
                placeholder="gravitybrewing"
                spellCheck={false}
                onChange={(e) => set('brand', e.target.value)}
              />
              <span className="field-note">
                The name in <code>untappd.com/…</code> — the brewery page, not
                the digital board. Without it the menu prints with no beer
                descriptions at all, because the board carries none. Kit reads
                each beer&rsquo;s own page once and remembers it.
                {data.notes_cached > 0 && (
                  <> {data.notes_cached} stored so far.</>
                )}
              </span>
            </label>
          </section>

          <section className="panel">
            <h2 className="panel-title">Wording</h2>
            <div className="field-row">
              <label className="field">
                <span>Masthead</span>
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
              <span>Line above the footer</span>
              <input
                value={cfg.flight ?? ''}
                placeholder="Try any set of four 4oz pours as a flight"
                onChange={(e) => set('flight', e.target.value)}
              />
            </label>
            <label className="field">
              <span>Pour sizes note</span>
              <input
                value={cfg.sizes ?? ''}
                placeholder="Full pours are 16oz unless marked — 9oz and 4oz also available"
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
            <h2 className="panel-title">Section colours</h2>
            <p className="card-desc">
              The headings on your tap list. Anything left unset cycles through
              the house colours, so a new section on the board still prints
              looking deliberate.
            </p>
            {sections.length === 0 ? (
              <p className="muted">
                No tap list yet — sections appear here once there is one.
              </p>
            ) : (
              sections.map((name) => (
                <div className="field-row" key={name}>
                  <label className="field">
                    <span>{name}</span>
                    <div className="drawer-actions">
                      <input
                        type="color"
                        aria-label={`${name} colour`}
                        value={cfg.colors?.[name] ?? '#cccccc'}
                        onChange={(e) => setColor(name, e.target.value)}
                      />
                      {data.palette.map((hex) => (
                        <button
                          key={hex}
                          type="button"
                          className="btn btn-ghost"
                          title={hex}
                          style={{ background: hex, minWidth: '2.5rem' }}
                          onClick={() => setColor(name, hex)}
                        >
                          {' '}
                        </button>
                      ))}
                      {cfg.colors?.[name] && (
                        <button
                          type="button"
                          className="btn btn-ghost"
                          onClick={() => setColor(name, '')}
                        >
                          Reset
                        </button>
                      )}
                    </div>
                  </label>
                </div>
              ))
            )}
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

          <section className="panel">
            <h2 className="panel-title">Not on tap</h2>
            <p className="card-desc">
              Cans, sodas and juice boxes — anything Untappd has no opinion
              about. A section whose rows are all packaged prints one price
              column instead of three.
            </p>
            {extras.map((row, i) => (
              <div className="field-row" key={i}>
                <label className="field">
                  <span>Section</span>
                  <input
                    value={row.section}
                    placeholder="Sodas & Juices"
                    list="menu-print-sections"
                    onChange={(e) => setExtra(i, { section: e.target.value })}
                  />
                </label>
                <label className="field">
                  <span>Name</span>
                  <input
                    value={row.name}
                    placeholder="Lemonade"
                    onChange={(e) => setExtra(i, { name: e.target.value })}
                  />
                </label>
                <label className="field">
                  <span>Style</span>
                  <input
                    value={row.style ?? ''}
                    placeholder="optional"
                    onChange={(e) => setExtra(i, { style: e.target.value })}
                  />
                </label>
                <label className="field">
                  <span>Size</span>
                  <input
                    value={row.pours?.[0]?.size ?? ''}
                    placeholder="12oz"
                    onChange={(e) => setExtraSize(i, e.target.value)}
                  />
                </label>
                <label className="field">
                  <span>Price</span>
                  <input
                    value={row.pours?.[0]?.price ?? ''}
                    placeholder="4.50"
                    onChange={(e) => setExtraPrice(i, e.target.value)}
                  />
                </label>
                <button
                  type="button"
                  className="btn btn-ghost"
                  onClick={() =>
                    set(
                      'extras',
                      extras.filter((_, j) => j !== i),
                    )
                  }
                >
                  Remove
                </button>
              </div>
            ))}
            <datalist id="menu-print-sections">
              {sections.map((s) => (
                <option key={s} value={s} />
              ))}
            </datalist>
            <div className="drawer-actions">
              <button
                type="button"
                className="btn btn-ghost"
                onClick={() =>
                  set('extras', [
                    ...extras,
                    { ...BLANK_EXTRA, pours: [{ ...BLANK_EXTRA.pours![0] }] },
                  ])
                }
              >
                Add a drink
              </button>
            </div>
          </section>

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
              The sheet is built when you open it, so save first and the next
              print has your changes.
            </p>
          </section>
        </>
      )}
    </div>
  );
}
