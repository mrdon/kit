import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type CreateSkillBody, type SkillSummary, type SkillsMeta } from '../api';
import { useDetailRoute } from '../useDetailRoute';
import { useMe } from '../me';
import { useSetChatContext } from '../chatContext';
import GroupedList, { type Group } from '../GroupedList';
import SkillDetail from './skills/detail';

// scopeLabel renders one scope as a human badge. tenant:* is "Public
// (website)" — it's the only tier the anonymous website widget can see — and
// the universal catchall role reads as "All members" (logged-in, not public).
function scopeLabel(sc: { type: string; value: string }, catchall: string): string {
  if (sc.type === 'tenant' || sc.type === 'platform') return 'Public (website)';
  if (sc.type === 'user') return 'Personal';
  if (sc.type === 'role' && sc.value === catchall) return 'All members';
  return sc.value;
}

// groupSkills buckets skills by audience tier. The order runs broadest to
// narrowest: Public (tenant:* — also visible to the website widget), then All
// members (the catchall role), then each specific role, then Personal, then
// built-ins as their own read-only group. A skill with several scopes shows
// in each matching group.
function groupSkills(skills: SkillSummary[], catchall: string): Group<SkillSummary>[] {
  const pub: SkillSummary[] = [];
  const members: SkillSummary[] = [];
  const builtin: SkillSummary[] = [];
  const personal: SkillSummary[] = [];
  const byRole = new Map<string, SkillSummary[]>();
  for (const s of skills) {
    if (s.builtin) {
      builtin.push(s);
      continue;
    }
    if (s.scopes.length === 0) pub.push(s);
    for (const sc of s.scopes) {
      if (sc.type === 'tenant' || sc.type === 'platform') {
        pub.push(s);
      } else if (sc.type === 'user') {
        personal.push(s);
      } else if (sc.value === catchall) {
        members.push(s);
      } else {
        const list = byRole.get(sc.value) ?? [];
        list.push(s);
        byRole.set(sc.value, list);
      }
    }
  }
  const groups: Group<SkillSummary>[] = [];
  if (pub.length) groups.push({ key: 'public', label: 'Public (website)', items: pub });
  if (members.length) groups.push({ key: 'members', label: 'All members', items: members });
  for (const role of [...byRole.keys()].sort()) {
    groups.push({ key: `role:${role}`, label: role, items: byRole.get(role)! });
  }
  if (personal.length) groups.push({ key: 'personal', label: 'Personal', items: personal });
  if (builtin.length) groups.push({ key: 'builtin', label: 'Built-in', items: builtin });
  return groups;
}

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

      <GroupedList
        groups={groupSkills(skills, meta?.catchall_role ?? 'member')}
        empty="No skills found."
        renderItem={(s) => (
          <li key={s.id || s.name}>
            <button className="entry-link" onClick={() => detail.open(s.id || s.name)}>
              <span className="entry-title">{s.name}</span>
              {s.description && <span className="entry-sub">{s.description}</span>}
              <span className="badge-row">
                {s.builtin && <span className="badge">built-in</span>}
                {!s.builtin &&
                  s.scopes.map((sc, i) => (
                    <span key={i} className="badge">
                      {scopeLabel(sc, meta?.catchall_role ?? 'member')}
                    </span>
                  ))}
              </span>
            </button>
          </li>
        )}
      />

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
  // Default to the catchall "All members" tier (logged-in only), never the
  // public website scope — publishing to the widget should be deliberate.
  const catchall = meta?.catchall_role ?? 'member';
  const [body, setBody] = useState<CreateSkillBody>({
    name: '',
    description: '',
    content: '',
    scope: catchall,
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
            <span>Who can see this?</span>
            <select value={body.scope} onChange={(e) => set('scope', e.target.value)}>
              <option value={catchall}>All members (your workspace)</option>
              <option value="tenant">Public — shown on your website ⚠</option>
              {meta?.roles
                .filter((r) => r !== catchall)
                .map((r) => (
                  <option key={r} value={r}>
                    Role: {r}
                  </option>
                ))}
            </select>
            {body.scope === 'tenant' && (
              <span className="field-hint">
                Public skills are visible to anyone on your website chat widget.
              </span>
            )}
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
