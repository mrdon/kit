import { useEffect, useState } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { api, type SkillDetail as Skill, type SkillsMeta, type UpdateSkillBody } from '../../api';

interface Props {
  skillId: string;
  isAdmin: boolean;
  meta: SkillsMeta | null;
  onClose: () => void;
  onChanged: () => void;
}

export default function SkillDetail({ skillId, isAdmin, meta, onClose, onChanged }: Props) {
  const [skill, setSkill] = useState<Skill | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const load = () => {
    api
      .getSkill(skillId)
      .then((r) => setSkill(r.skill))
      .catch((e) => setErr(e.message));
  };
  useEffect(load, [skillId]);

  const editable = !!skill?.editable && isAdmin;

  const patch = async (body: UpdateSkillBody) => {
    if (!skill) return;
    setErr(null);
    try {
      await api.updateSkill(skill.id, body);
      onChanged();
      load();
    } catch (e) {
      setErr((e as Error).message);
    }
  };

  const remove = async () => {
    if (!skill || !confirm(`Delete skill "${skill.name}"?`)) return;
    try {
      await api.deleteSkill(skill.id);
      onChanged();
      onClose();
    } catch (e) {
      setErr((e as Error).message);
    }
  };

  return (
    <div className="drawer-backdrop" onClick={onClose}>
      <aside className="drawer" onClick={(e) => e.stopPropagation()}>
        <button className="drawer-close" onClick={onClose} aria-label="Close">
          ×
        </button>
        {err && <p className="banner banner-error">{err}</p>}
        {!skill ? (
          <p className="muted">Loading…</p>
        ) : (
          <>
            {skill.builtin && <p className="banner">Built-in skill — read only.</p>}
            <EditableText
              value={skill.name}
              editable={editable}
              className="drawer-title"
              onSave={(v) => patch({ name: v })}
              render={(v) => <h2 className="drawer-title">{v}</h2>}
            />
            <EditableText
              value={skill.description}
              editable={editable}
              onSave={(v) => patch({ description: v })}
              render={(v) => <p className="muted">{v || 'No description.'}</p>}
            />
            <EditableBody
              content={skill.content}
              editable={editable}
              onSave={(v) => patch({ content: v })}
            />
            {editable && (
              <label className="field">
                <span>Who can see this?</span>
                <select value={skill.scope} onChange={(e) => patch({ scope: e.target.value })}>
                  <option value={meta?.catchall_role ?? 'member'}>
                    All members (your workspace)
                  </option>
                  <option value="tenant">Public — shown on your website ⚠</option>
                  {(meta?.roles ?? [])
                    .filter((r) => r !== (meta?.catchall_role ?? 'member'))
                    .map((r) => (
                      <option key={r} value={r}>
                        Role: {r}
                      </option>
                    ))}
                </select>
                {skill.scope === 'tenant' && (
                  <span className="field-hint">
                    Visible to anyone on your website chat widget.
                  </span>
                )}
              </label>
            )}
            {!skill.builtin && (
              <Files
                skillId={skill.id}
                files={skill.files}
                editable={editable}
                onChanged={load}
              />
            )}
            {editable && (
              <div className="drawer-actions">
                <button className="btn btn-danger" onClick={remove}>
                  Delete skill
                </button>
              </div>
            )}
          </>
        )}
      </aside>
    </div>
  );
}

// EditableText shows a value; click to edit inline (admins, editable skills).
function EditableText({
  value,
  editable,
  className,
  onSave,
  render,
}: {
  value: string;
  editable: boolean;
  className?: string;
  onSave: (v: string) => void;
  render: (v: string) => React.ReactNode;
}) {
  const [editing, setEditing] = useState(false);
  if (editable && editing) {
    return (
      <input
        className={className}
        autoFocus
        defaultValue={value}
        onBlur={(e) => {
          setEditing(false);
          const v = e.target.value.trim();
          if (v !== value) onSave(v);
        }}
      />
    );
  }
  return (
    <div
      title={editable ? 'Click to edit' : undefined}
      className={editable ? 'view-title' : undefined}
      onClick={() => editable && setEditing(true)}
    >
      {render(value)}
    </div>
  );
}

// EditableBody renders content as markdown; click to edit raw (when editable).
function EditableBody({
  content,
  editable,
  onSave,
}: {
  content: string;
  editable: boolean;
  onSave: (v: string) => void;
}) {
  const [editing, setEditing] = useState(false);
  if (editable && editing) {
    return (
      <textarea
        className="body-edit"
        autoFocus
        rows={16}
        defaultValue={content}
        onBlur={(e) => {
          setEditing(false);
          if (e.target.value !== content) onSave(e.target.value);
        }}
      />
    );
  }
  return (
    <div
      className="md-body markdown"
      title={editable ? 'Click to edit' : undefined}
      onClick={() => editable && setEditing(true)}
    >
      {content ? (
        <ReactMarkdown remarkPlugins={[remarkGfm]}>{content}</ReactMarkdown>
      ) : (
        <p className="muted">No content{editable ? ' — click to add.' : '.'}</p>
      )}
    </div>
  );
}

// Files is the attached-files list with add/delete for admins.
function Files({
  skillId,
  files,
  editable,
  onChanged,
}: {
  skillId: string;
  files: { id: string; filename: string }[];
  editable: boolean;
  onChanged: () => void;
}) {
  const [filename, setFilename] = useState('');
  const [content, setContent] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const add = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!filename.trim() || !content.trim()) return;
    setBusy(true);
    setErr(null);
    try {
      await api.addSkillFile(skillId, filename.trim(), content);
      setFilename('');
      setContent('');
      onChanged();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const remove = async (fileId: string) => {
    try {
      await api.deleteSkillFile(fileId);
      onChanged();
    } catch (e) {
      setErr((e as Error).message);
    }
  };

  return (
    <>
      <h3 className="timeline-head">Files</h3>
      {err && <p className="banner banner-error">{err}</p>}
      <ul className="entry-list">
        {files.map((f) => (
          <li key={f.id} className="file-row">
            <span className="entry-title">{f.filename}</span>
            {editable && (
              <button className="btn btn-ghost" onClick={() => remove(f.id)}>
                Remove
              </button>
            )}
          </li>
        ))}
        {files.length === 0 && <li className="muted">No files attached.</li>}
      </ul>
      {editable && (
        <form onSubmit={add} className="stack-form">
          <label className="field">
            <span>Filename</span>
            <input
              placeholder="setup.sh"
              value={filename}
              onChange={(e) => setFilename(e.target.value)}
            />
          </label>
          <label className="field">
            <span>Content</span>
            <textarea rows={4} value={content} onChange={(e) => setContent(e.target.value)} />
          </label>
          <div className="drawer-actions">
            <button className="btn" type="submit" disabled={busy}>
              {busy ? 'Adding…' : 'Add file'}
            </button>
          </div>
        </form>
      )}
    </>
  );
}
