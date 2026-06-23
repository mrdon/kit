import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type JobView } from '../api';
import { useDetailRoute } from '../useDetailRoute';
import { useSetChatContext } from '../chatContext';
import JobDetail from './jobs/detail';

export default function Jobs() {
  const [jobs, setJobs] = useState<JobView[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const detail = useDetailRoute('/jobs');

  const load = useCallback(() => {
    api
      .listJobs()
      .then((r) => setJobs(r.jobs))
      .catch((e) => setErr(e.message));
  }, []);
  useEffect(load, [load]);

  useSetChatContext(
    detail.openId ? 'the Jobs page, viewing a job' : 'the Jobs page',
    load,
  );

  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <span>Jobs</span>
        </nav>
        <div className="page-head-row">
          <h1>Jobs</h1>
        </div>
        <p className="page-sub">
          Scheduled work Kit runs for you. Create jobs by asking Kit in chat.
        </p>
      </div>

      {err && <p className="banner banner-error">{err}</p>}

      <ul className="entry-list">
        {jobs.map((j) => (
          <li key={j.id}>
            <button className="entry-link" onClick={() => detail.open(j.id)}>
              <span className="entry-title">{j.description || '(untitled job)'}</span>
              <span className="entry-sub">
                {j.schedule} · {j.status}
              </span>
              <span className="badge-row">
                {j.skill_name && <span className="badge">skill: {j.skill_name}</span>}
                {j.policy_summary && <span className="badge">{j.policy_summary}</span>}
                {j.last_error && <span className="badge badge-error">last run failed</span>}
              </span>
            </button>
          </li>
        ))}
        {jobs.length === 0 && <li className="muted">No jobs yet.</li>}
      </ul>

      {detail.openId && (
        <JobDetail jobId={detail.openId} onClose={detail.close} onChanged={load} />
      )}
    </div>
  );
}
