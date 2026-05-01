import { useCallback, useEffect, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import {
  animate,
  motion,
  useMotionValue,
  useTransform,
  type PanInfo,
} from 'framer-motion';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { api } from './api';
import type { DetailResponse, StackItem, JobStatus } from './types';
import { itemKey } from './types';
import { rendererFor } from './kinds';
import ErrorBoundary from './ErrorBoundary';
import CardChatSheet from './chat/CardChatSheet';

export default function StackItemDetail() {
  const params = useParams<{ source_app: string; kind: string; id: string }>();
  const navigate = useNavigate();
  const [item, setItem] = useState<StackItem | null>(null);
  const [extras, setExtras] = useState<Record<string, unknown> | undefined>();
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [chatOpen, setChatOpen] = useState(false);

  const load = useCallback(async () => {
    if (!params.source_app || !params.kind || !params.id) return;
    try {
      const resp: DetailResponse = await api.getItem(
        params.source_app,
        params.kind,
        params.id,
      );
      setItem(resp.item);
      setExtras(resp.extras);
      setErr(null);
    } catch (e) {
      setErr((e as Error).message);
    }
  }, [params.source_app, params.kind, params.id]);

  useEffect(() => {
    load();
  }, [load]);

  // Poll the linked agent task (decision cards) while it's still running.
  useEffect(() => {
    const task = extras?.task as JobStatus | undefined;
    if (!task) return;
    if (task.status !== 'active' && task.status !== 'running') return;
    const t = setInterval(load, 4000);
    return () => clearInterval(t);
  }, [extras, load]);

  // ESC pops back to the stack — hard-keyboard and desktop testing
  // escape route when a render error or stuck gesture leaves the page
  // feeling trapped.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') navigate('/');
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [navigate]);

  const onAction = async (actionID: string, actionParams?: unknown) => {
    if (busy || !item) return;
    setBusy(true);
    try {
      await api.doAction(
        item.source_app,
        item.kind,
        item.id,
        actionID,
        actionParams,
      );
      // Wake puts the todo BACK into the active feed — hoist it to
      // the top via the focus hash so the user sees the thing they
      // just woke, not the feed root. Other actions remove the item,
      // so a plain "/" is right for them.
      if (actionID === 'wake') {
        navigate(`/#${itemKey(item)}`);
      } else {
        navigate('/');
      }
    } catch (e) {
      alert((e as Error).message);
      setBusy(false);
    }
  };

  if (err) return <main className="detail">Error: {err}</main>;
  if (!item) return <main className="detail">Loading…</main>;

  const renderer = rendererFor(item);
  const Detail = renderer.Detail;

  return (
    <SwipeBackShell item={item}>
      <Link to="/" className="back" aria-label="Back to stack">
        ← Back
      </Link>
      <div className="kind-tag">
        {item.icon ? `${item.icon} ` : ''}
        {item.kind_label}
      </div>
      <h1>{item.title}</h1>
      {item.badges && item.badges.length > 0 && (
        <div className="badges">
          {item.badges.map((b, i) => (
            <span key={i} className={`badge tone-${b.tone}`}>
              {b.label}
            </span>
          ))}
        </div>
      )}
      {item.recommended_next_step && (
        <>
          <div className={`recommended-next-step kind-${item.recommended_next_step.kind}`}>
            <div className="recommended-next-step-eyebrow">Recommended next step</div>
            <div className="recommended-next-step-label">
              {item.recommended_next_step.kind === 'task' ? '✨ ' : '💡 '}
              {item.recommended_next_step.label}
            </div>
            {item.recommended_next_step.body && (
              <div className="recommended-next-step-body">
                {item.recommended_next_step.body}
              </div>
            )}
          </div>
          <hr className="recommended-next-step-divider" />
        </>
      )}
      {item.body && (
        <div className="body markdown">
          <ReactMarkdown remarkPlugins={[remarkGfm]}>{item.body}</ReactMarkdown>
        </div>
      )}
      {Detail && (
        <ErrorBoundary>
          <Detail
            item={item}
            extras={extras}
            onAction={onAction}
            onRefresh={load}
            busy={busy}
          />
        </ErrorBoundary>
      )}
      <div className="detail-chat-row">
        <button
          type="button"
          className="detail-chat-button"
          onClick={() => setChatOpen(true)}
        >
          💬 Chat about this
        </button>
      </div>
      {chatOpen && (
        <CardChatSheet
          sourceApp={item.source_app}
          kind={item.kind}
          id={item.id}
          title={item.title}
          onClose={() => {
            setChatOpen(false);
            load();
          }}
          onTurnDone={load}
        />
      )}
    </SwipeBackShell>
  );
}

// SwipeBackShell makes the whole detail view drag-dismissable. Either
// direction past the threshold pops back to the stack — the same
// gesture a native app uses for "pop view controller" but accepting
// both sides so you can swipe with whichever thumb is free.
function SwipeBackShell({
  item,
  children,
}: {
  item: StackItem;
  children: React.ReactNode;
}) {
  const navigate = useNavigate();
  const x = useMotionValue(0);
  const threshold =
    typeof window !== 'undefined' ? Math.max(180, window.innerWidth * 0.35) : 180;
  const opacity = useTransform(x, [-threshold, 0, threshold], [0.3, 1, 0.3]);

  const onDragEnd = (_e: unknown, info: PanInfo) => {
    const vw = typeof window !== 'undefined' ? window.innerWidth : 800;
    if (info.offset.x < -threshold) {
      animate(x, -vw, {
        duration: 0.2,
        onComplete: () => navigate('/'),
      });
      return;
    }
    if (info.offset.x > threshold) {
      animate(x, vw, {
        duration: 0.2,
        onComplete: () => navigate('/'),
      });
      return;
    }
    animate(x, 0, { type: 'spring', stiffness: 500, damping: 32 });
  };

  const vw = typeof window !== 'undefined' ? window.innerWidth : 800;
  return (
    <motion.main
      className={`detail tier-${item.priority_tier}`}
      drag="x"
      dragConstraints={{ left: -vw, right: vw }}
      dragElastic={0.25}
      style={{ x, opacity }}
      onDragEnd={onDragEnd}
    >
      {children}
    </motion.main>
  );
}
