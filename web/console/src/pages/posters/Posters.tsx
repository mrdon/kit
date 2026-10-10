import { useCallback, useEffect, useMemo, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { api, type Poster } from '../../api';
import { useSetChatContext } from '../../chatContext';
import { PostersTabs, errText, fmtWhen, statusPill } from './common';

// Every poster, grouped by event. One poster per event (a repeating event
// has one for the series), so a group is normally one row; the grouping is
// there for the day two people make a poster for the same thing.

export default function Posters() {
  const [posters, setPosters] = useState<Poster[] | null>(null);
  const [rendererReady, setRendererReady] = useState(true);
  const [err, setErr] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const navigate = useNavigate();

  const load = useCallback(() => {
    api
      .listPosters()
      .then((r) => {
        setPosters(r.posters ?? []);
        setRendererReady(r.renderer_ready);
      })
      .catch((e) => setErr(errText(e)));
  }, []);
  useEffect(load, [load]);
  useSetChatContext('the Posters page', load);

  const groups = useMemo(() => {
    const by = new Map<string, { title: string; items: Poster[] }>();
    for (const p of posters ?? []) {
      const key = p.event_id ?? `poster:${p.id}`;
      const g = by.get(key) ?? { title: p.event_title || p.title || 'Untitled', items: [] };
      g.items.push(p);
      by.set(key, g);
    }
    return [...by.values()];
  }, [posters]);

  const remove = async (p: Poster) => {
    const name = p.event_title || p.title || 'this poster';
    if (!confirm(`Delete the poster for "${name}" and all its versions? The event keeps the image already on it.`)) return;
    try {
      await api.deletePoster(p.id);
      setNote('Poster deleted.');
      load();
    } catch (e) {
      setErr(errText(e));
    }
  };

  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <span>Posters</span>
        </nav>
        <h1>Posters</h1>
        <p className="page-sub">
          Generate a poster from an event's drawer; come here to edit it, download other
          sizes, or turn a good one into a template.
        </p>
      </div>
      <PostersTabs />

      {note && (
        <p className="banner banner-ok" onClick={() => setNote(null)}>
          {note}
        </p>
      )}
      {err && <p className="banner banner-error">{err}</p>}
      {!rendererReady && (
        <p className="banner banner-error">
          The poster renderer is not running; previews will not load until it is back.
        </p>
      )}
      {posters === null && !err && <p className="muted">Loading…</p>}
      {posters && posters.length === 0 && (
        <p className="empty">
          No posters yet. Open an event under <Link to="/events">Events</Link> and press
          Generate poster.
        </p>
      )}

      {groups.map((g) => (
        <section key={g.title + g.items[0].id} className="group">
          <h2 className="group-head">{g.title}</h2>
          <ul className="card-list">
            {g.items.map((p) => (
              <li key={p.id} className="card poster-row">
                <button
                  type="button"
                  className="poster-row-main"
                  onClick={() => navigate(`/posters/${p.id}`)}
                >
                  <span className="poster-row-thumb">
                    {p.thumb ? (
                      <img src={p.thumb} alt="" loading="lazy" />
                    ) : (
                      <span className="poster-row-blank">No version</span>
                    )}
                  </span>
                  <span className="card-main">
                    <span className="card-title">{p.title || g.title}</span>
                    <span className="card-desc">Updated {fmtWhen(p.updated_at)}</span>
                  </span>
                </button>
                <div className="card-side">
                  <span className={statusPill(p.status)}>{p.status}</span>
                  <button type="button" className="btn btn-ghost btn-sm" onClick={() => remove(p)}>
                    Delete
                  </button>
                </div>
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}
