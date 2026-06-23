import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type CreateSkillBody, type SkillSummary, type SkillsMeta } from '../api';
import { useDetailRoute } from '../useDetailRoute';
import { useMe } from '../me';
import { useSetChatContext } from '../chatContext';
import SkillDetail from './skills/detail';

export default function Skills() {
  const me = useMe();
  const isAdmin = !!me?.is_admin;
  const [skills, setSkills] = useState<SkillSummary[]>([]);
  const [meta, setMeta] = useState<SkillsMeta | null>(null);
  const [search, setSearch] = useState('');
  const [err, setErr] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const detail = useDetailRoute('/skills');

  useEffect(() => {
    api.skillsMeta().then(setMeta).catch(() => {});
  }, []);

  const load = useCallback(() => {
    api
      .listSkills(search)
      .then((r) => setSkills(r.skills))
      .catch((e) => setErr(e.message));
  }, [search]);
  useEffect(load, [load]);

  useSetChatContext(
    detail.openId ? `the Skills page, viewing a skill` : 'the Skills page',
    load,
  );

  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <span>Skills</span>
        </nav>
        <div className="page-head-row">
          <h1>Skills</h1>
          {isAdmin && (
            <div className="page-head-actions">
              <button className="btn" onClick={() => setCreating(true)}>
                New skill
              </button>
            </div>
          )}
        </div>
      </div>

      {err && <p className="banner banner-error">{err}</p>}

      <div className="toolbar">
        <input
          placeholder="Search skills…"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
      </div>

      <ul className="entry-list">
        {skills.map((s) => (
          <li key={s.id || s.name}>
            <button className="entry-link" onClick={() => detail.open(s.id || s.name)}>
              <span className="entry-title">{s.name}</span>
              {s.description && <span className="entry-sub">{s.description}</span>}
              <span className="badge-row">
                {s.builtin && <span className="badge">built-in</span>}
                {s.scopes.map((sc, i) => (
                  <span key={i} className="badge">
                    {sc.type === 'platform' ? 'everyone' : `${sc.type}:${sc.value}`}
                  </span>
                ))}
              </span>
            </button>
          </li>
        ))}
        {skills.length === 0 && <li className="muted">No skills found.</li>}
      </ul>

      {detail.openId && (
        <SkillDetail
          skillId={detail.openId}
          isAdmin={isAdmin}
          onClose={detail.close}
          onChanged={load}
        />
      )}

      {creating && (
        <CreateSkill
          meta={meta}
          onClose={() => setCreating(false)}
          onCreated={(id) => {
            setCreating(false);
            load();
            detail.open(id);
          }}
        />
      )}
    </div>
  );
}

function CreateSkill({
  meta,
  onClose,
  onCreated,
}: {
  meta: SkillsMeta | null;
  onClose: () => void;
  onCreated: (id: string) => void;
}) {
  const [body, setBody] = useState<CreateSkillBody>({
    name: '',
    description: '',
    content: '',
    scope: 'tenant',
  });
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const set = (k: keyof CreateSkillBody, v: string) =>
    setBody((b) => ({ ...b, [k]: v }));

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    try {
      const r = await api.createSkill(body);
      onCreated(r.skill.id);
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="drawer-backdrop" onClick={onClose}>
      <aside className="drawer" onClick={(e) => e.stopPropagation()}>
        <button className="drawer-close" onClick={onClose} aria-label="Close">
          ×
        </button>
        <h2 className="panel-title">New skill</h2>
        {err && <p className="banner banner-error">{err}</p>}
        <form onSubmit={submit} className="stack-form">
          <label className="field">
            <span>Name</span>
            <input
              required
              autoFocus
              placeholder="lowercase-with-hyphens"
              onChange={(e) => set('name', e.target.value)}
            />
          </label>
          <label className="field">
            <span>Description</span>
            <input required onChange={(e) => set('description', e.target.value)} />
          </label>
          <label className="field">
            <span>Scope</span>
            <select value={body.scope} onChange={(e) => set('scope', e.target.value)}>
              <option value="tenant">Everyone (tenant)</option>
              {meta?.roles.map((r) => (
                <option key={r} value={r}>
                  {r}
                </option>
              ))}
            </select>
          </label>
          <label className="field">
            <span>Content (markdown)</span>
            <textarea required rows={10} onChange={(e) => set('content', e.target.value)} />
          </label>
          <div className="drawer-actions">
            <button className="btn" type="submit" disabled={busy}>
              {busy ? 'Creating…' : 'Create skill'}
            </button>
          </div>
        </form>
      </aside>
    </div>
  );
}
