import { useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type MenuPrintBeer } from '../../api';
import { useSetChatContext } from '../../chatContext';
import { usePolled } from '../../usePolled';

// Beer descriptions for the printed menu, from the bar iPad. Untappd's
// board carries no prose and its pages refuse Kit's server, so most of
// these are typed here. One box per beer on tap, saved per beer, with a
// line saying whether the current text was written here or scraped.
//
// Polling merges: a beer whose box has been edited keeps the edit; only
// untouched boxes follow the server, so two people on two iPads see each
// other's work without either losing a sentence mid-type.

export default function DeviceDescriptions() {
  useSetChatContext('the beer descriptions page on a taproom device');
  const [beers, setBeers] = useState<MenuPrintBeer[] | null>(null);
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [savedName, setSavedName] = useState<string | null>(null);

  const apply = (next: MenuPrintBeer[]) => {
    setBeers((prev) => {
      setDrafts((d) => {
        const out: Record<string, string> = {};
        for (const b of next) {
          const was = prev?.find((p) => p.name === b.name);
          const touched = d[b.name] !== undefined && d[b.name] !== (was?.note ?? '');
          out[b.name] = touched ? d[b.name] : b.note;
        }
        return out;
      });
      return next;
    });
  };

  const load = () =>
    api
      .menuPrintNotes()
      .then((r) => apply(r.beers))
      .catch((e) => setErr(e instanceof Error ? e.message : String(e)));
  const reload = usePolled(load);

  async function save(b: MenuPrintBeer) {
    setBusy(b.name);
    setErr(null);
    setSavedName(null);
    try {
      const r = await api.saveMenuPrintNote(b.name, drafts[b.name] ?? '');
      setDrafts((d) => ({ ...d, [b.name]: r.beers.find((x) => x.name === b.name)?.note ?? '' }));
      setBeers(r.beers);
      setSavedName(b.name);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  }

  const sections = beers ? Array.from(new Set(beers.map((b) => b.section))) : [];

  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <Link to="/device/print">Print the menu</Link>
          <span className="crumb-sep">/</span>
          <span>Descriptions</span>
        </nav>
        <h1>Beer descriptions</h1>
        <p className="page-sub">
          What prints under each beer. Untappd rarely has it, so write it here; a description you write stays through syncs.
        </p>
      </div>
      {err && <p className="banner banner-error">{err}</p>}
      {beers && beers.length === 0 && (
        <p className="device-empty">
          No beers yet. <button className="btn" type="button" onClick={reload}>Refresh the tap list</button> on the print page first.
        </p>
      )}
      {sections.map((section) => (
        <section className="panel" key={section}>
          <h2 className="panel-title">{section}</h2>
          <ul className="card-list">
            {beers?.filter((b) => b.section === section).map((b) => {
              const draft = drafts[b.name] ?? '';
              const dirty = draft !== b.note;
              return (
                <li key={b.name} className="card">
                  <div className="card-main">
                    <div className="card-title">
                      {b.name}
                      {b.style ? <span className="muted"> · {b.style}</span> : null}
                    </div>
                    <textarea
                      className="device-note"
                      rows={3}
                      value={draft}
                      placeholder="No description yet — write one"
                      onChange={(e) => setDrafts({ ...drafts, [b.name]: e.target.value })}
                    />
                    <div className="page-head-actions">
                      <button className="btn device-btn-sm" type="button" disabled={busy === b.name || !dirty} onClick={() => void save(b)}>
                        {busy === b.name ? 'Saving…' : savedName === b.name && !dirty ? 'Saved' : 'Save'}
                      </button>
                      <span className="muted">
                        {b.note === '' ? 'Nothing printed yet' : b.written ? 'Written here' : 'From Untappd'}
                      </span>
                    </div>
                  </div>
                </li>
              );
            })}
          </ul>
        </section>
      ))}
    </div>
  );
}
