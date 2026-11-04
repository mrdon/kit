import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type QueueItem } from '../../../api';
import { useSetChatContext } from '../../../chatContext';
import { Crumbs } from './common';

const KIND_LABELS: Record<string, string> = {
  new_beer: 'New beer',
  plan_flag: 'Plan date flagged',
  count_variance: 'Count didn’t match',
};

// Loose ends the brewer's entries left for the console: a beer typed on the
// iPad, a plan date that won't work, a count that didn't add up. None of
// them blocked the brewer; each waits here until someone deals with it.
export default function CogsQueue() {
  useSetChatContext('the brewing queue page');
  const [items, setItems] = useState<QueueItem[] | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const load = () =>
    api
      .brewQueue()
      .then((r) => setItems(r.items))
      .catch((e) => setErr((e as Error).message));
  useEffect(() => {
    void load();
  }, []);

  const resolve = async (id: string) => {
    setErr(null);
    try {
      await api.brewResolveQueue(id);
      void load();
    } catch (e) {
      setErr((e as Error).message);
    }
  };

  return (
    <div className="page brew-admin">
      <div className="page-head">
        <Crumbs page="Queue" />
        <h1>Brewing queue</h1>
        <p className="page-sub">Things the brewer's log left for you to finish.</p>
      </div>
      {err && <p className="banner banner-error">{err}</p>}
      {items && items.length === 0 && <p className="muted">Nothing waiting.</p>}
      <ul className="card-list">
        {items?.map((it) => (
          <li key={it.id} className="card">
            <div className="card-main">
              <div className="card-title">
                <span className="pill pill-off">{KIND_LABELS[it.kind] ?? it.kind}</span> {it.title}
              </div>
              {it.body && <div className="card-desc brew-prewrap">{it.body}</div>}
              <div className="muted brew-small">
                {it.created_label && `${it.created_label}, `}
                {new Date(it.created_at).toLocaleString(undefined, {
                  weekday: 'short',
                  month: 'short',
                  day: 'numeric',
                  hour: 'numeric',
                  minute: '2-digit',
                })}
              </div>
            </div>
            <div className="brew-row-actions">
              {it.kind === 'new_beer' && (
                <Link className="btn btn-ghost btn-sm" to="/cogs/setup">
                  Set it up
                </Link>
              )}
              {it.kind === 'plan_flag' && (
                <Link className="btn btn-ghost btn-sm" to="/cogs/plan">
                  Open the plan
                </Link>
              )}
              <button className="btn btn-sm" type="button" onClick={() => void resolve(it.id)}>
                Done
              </button>
            </div>
          </li>
        ))}
      </ul>
    </div>
  );
}
