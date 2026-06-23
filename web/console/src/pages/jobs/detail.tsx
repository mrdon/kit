import { useEffect, useState } from 'react';
import {
  api,
  type JobPolicy,
  type JobView,
  type SkillSummary,
  type UpdateJobBody,
} from '../../api';

interface Props {
  jobId: string;
  onClose: () => void;
  onChanged: () => void;
}

function fmt(ts?: string): string {
  if (!ts) return '—';
  const d = new Date(ts);
  return isNaN(d.getTime()) ? ts : d.toLocaleString();
}

export default function JobDetail({ jobId, onClose, onChanged }: Props) {
  const [job, setJob] = useState<JobView | null>(null);
  const [skills, setSkills] = useState<SkillSummary[]>([]);
  const [err, setErr] = useState<string | null>(null);

  const load = () => {
    api
      .getJob(jobId)
      .then((r) => setJob(r.job))
      .catch((e) => setErr(e.message));
  };
  useEffect(load, [jobId]);
  useEffect(() => {
    api.listSkills().then((r) => setSkills(r.skills)).catch(() => {});
  }, []);

  const patch = async (body: UpdateJobBody) => {
    setErr(null);
    try {
      const r = await api.updateJob(jobId, body);
      setJob(r.job);
      onChanged();
    } catch (e) {
      setErr((e as Error).message);
    }
  };

  const remove = async () => {
    if (!job || !confirm('Delete this job?')) return;
    try {
      await api.deleteJob(jobId);
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
        {!job ? (
          <p className="muted">Loading…</p>
        ) : (
          <>
            <h2 className="drawer-title">{job.description || '(untitled job)'}</h2>
            {!job.editable && (
              <p className="banner">Built-in job — read only.</p>
            )}
            <Meta job={job} />
            {job.editable && (
              <>
                <label className="field">
                  <span>Description</span>
                  <textarea
                    rows={2}
                    defaultValue={job.description}
                    onBlur={(e) =>
                      e.target.value !== job.description &&
                      patch({ description: e.target.value })
                    }
                  />
                </label>

                <label className="field">
                  <span>Linked skill</span>
                  <select
                    value={job.skill_name}
                    onChange={(e) => patch({ skill_name: e.target.value })}
                  >
                    <option value="">(none — run description as prompt)</option>
                    {job.skill_name &&
                      !skills.some((s) => s.name === job.skill_name) && (
                        <option value={job.skill_name}>{job.skill_name}</option>
                      )}
                    {skills
                      .filter((s) => !s.builtin)
                      .map((s) => (
                        <option key={s.name} value={s.name}>
                          {s.name}
                        </option>
                      ))}
                  </select>
                </label>

                <PolicyEditor policy={job.policy} onSave={(p) => patch({ policy: p })} />

                <div className="drawer-actions">
                  <button className="btn btn-danger" onClick={remove}>
                    Delete job
                  </button>
                </div>
              </>
            )}
          </>
        )}
      </aside>
    </div>
  );
}

function Meta({ job }: { job: JobView }) {
  return (
    <div className="task-props">
      <Row label="Schedule" value={job.schedule} />
      <Row label="Status" value={job.status} />
      <Row label="Model" value={job.model} />
      <Row label="Next run" value={fmt(job.next_run_at)} />
      <Row label="Last run" value={fmt(job.last_run_at)} />
      {job.last_error && <Row label="Last error" value={job.last_error} />}
    </div>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="field-row">
      <span className="muted">{label}</span>
      <span>{value}</span>
    </div>
  );
}

// PolicyEditor edits the capability manifest as raw JSON. Policy only ever
// constrains the scheduled agent, so a free-form JSON textarea (validated on
// save) is enough — no structured builder needed.
function PolicyEditor({
  policy,
  onSave,
}: {
  policy: JobPolicy | null;
  onSave: (p: JobPolicy) => void;
}) {
  const initial = policy ? JSON.stringify(policy, null, 2) : '';
  const [text, setText] = useState(initial);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => setText(policy ? JSON.stringify(policy, null, 2) : ''), [policy]);

  const save = () => {
    setErr(null);
    let parsed: JobPolicy;
    try {
      parsed = text.trim() ? JSON.parse(text) : {};
    } catch {
      setErr('Invalid JSON.');
      return;
    }
    onSave(parsed);
  };

  return (
    <details className="policy-editor">
      <summary>Advanced — policy (JSON)</summary>
      {err && <p className="banner banner-error">{err}</p>}
      <textarea
        className="body-edit"
        rows={8}
        value={text}
        placeholder='{ "allowed_tools": ["send_slack_message"] }'
        onChange={(e) => setText(e.target.value)}
      />
      <div className="drawer-actions">
        <button className="btn" onClick={save}>
          Save policy
        </button>
      </div>
    </details>
  );
}
