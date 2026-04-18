import { useCallback, useEffect, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { api } from './api';
import type { Card, TaskStatus } from './types';

export default function CardDetail() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [card, setCard] = useState<Card | null>(null);
  const [task, setTask] = useState<TaskStatus | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    if (!id) return;
    try {
      const resp = await api.card(id);
      setCard(resp.card);
      setTask(resp.task ?? null);
      setErr(null);
    } catch (e) {
      setErr((e as Error).message);
    }
  }, [id]);

  useEffect(() => { load(); }, [load]);

  // Poll task status every 4s while a task is linked and running.
  useEffect(() => {
    if (!task) return;
    if (task.status !== 'active' && task.status !== 'running') return;
    const t = setInterval(load, 4000);
    return () => clearInterval(t);
  }, [task, load]);

  if (err) return <main className="detail">Error: {err}</main>;
  if (!card) return <main className="detail">Loading…</main>;

  const onResolve = async (optionID: string) => {
    if (busy || !id) return;
    setBusy(true);
    try {
      await api.resolve(id, optionID);
      navigate('/');
    } catch (e) {
      alert((e as Error).message);
      setBusy(false);
    }
  };

  const onAck = async (kind: 'archived' | 'dismissed' | 'saved') => {
    if (busy || !id) return;
    setBusy(true);
    try {
      await api.ack(id, kind);
      navigate('/');
    } catch (e) {
      alert((e as Error).message);
      setBusy(false);
    }
  };

  const isTerminal = card.state !== 'pending';

  return (
    <main className="detail">
      <Link to="/" className="back">← Back</Link>
      <h1>{card.title}</h1>
      <div className="body markdown">
        <ReactMarkdown remarkPlugins={[remarkGfm]}>{card.body}</ReactMarkdown>
      </div>

      {card.kind === 'decision' && card.decision && !isTerminal && (
        <div className="options">
          {card.decision.options.map((o) => (
            <button
              key={o.option_id}
              disabled={busy}
              onClick={() => onResolve(o.option_id)}
              className={o.option_id === card.decision?.recommended_option_id ? 'recommended' : ''}
            >
              <div className="label">{o.label}</div>
              {o.prompt && <div className="prompt">{o.prompt}</div>}
            </button>
          ))}
        </div>
      )}

      {card.kind === 'briefing' && !isTerminal && (
        <div className="acks">
          <button disabled={busy} onClick={() => onAck('archived')}>👍 Useful</button>
          <button disabled={busy} onClick={() => onAck('dismissed')}>👎 Not useful</button>
        </div>
      )}

      {isTerminal && (
        <div className="task-status">
          This card was {card.state}.
          {card.decision?.resolved_option_id && ` Chose: ${card.decision.resolved_option_id}.`}
        </div>
      )}

      {task && (
        <div className="task-status">
          <div><strong>Kit's task</strong></div>
          <div>Status: {task.status}</div>
          {task.last_run_at && <div>Last run: {new Date(task.last_run_at).toLocaleString()}</div>}
          {task.last_error && <div style={{ color: '#f87171' }}>Error: {task.last_error}</div>}
        </div>
      )}
    </main>
  );
}
