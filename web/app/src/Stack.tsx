import { useCallback, useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  animate,
  AnimatePresence,
  motion,
  useMotionValue,
  useMotionValueEvent,
  useTransform,
  type PanInfo,
} from 'framer-motion';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { api } from './api';
import type { StackAction, StackItem, StackResponse } from './types';
import { itemKey } from './types';
import { rendererFor } from './kinds';

type CommitDirection = 'right' | 'left';

export default function Stack() {
  const [items, setItems] = useState<StackItem[] | null>(null);
  const [degraded, setDegraded] = useState<StackResponse['degraded']>([]);
  const [err, setErr] = useState<string | null>(null);
  const [burst, setBurst] = useState<{ id: string; emoji: string } | null>(null);
  const [progress, setProgress] = useState(0);
  const feedRef = useRef<HTMLElement | null>(null);

  const onScroll = useCallback(() => {
    const el = feedRef.current;
    if (!el) return;
    const max = el.scrollHeight - el.clientHeight;
    setProgress(max > 0 ? el.scrollTop / max : 0);
  }, []);

  // Keep progress in sync when the item list changes (completions shrink
  // the scroll height; without a recompute the thumb drifts stale).
  useEffect(() => {
    onScroll();
  }, [items, onScroll]);

  const load = useCallback(async () => {
    try {
      const resp = await api.stack();
      setItems(resp.items ?? []);
      setDegraded(resp.degraded ?? []);
      setErr(null);
    } catch (e) {
      setErr((e as Error).message);
    }
  }, []);

  useEffect(() => {
    load();
    const onFocus = () => load();
    window.addEventListener('focus', onFocus);
    document.addEventListener('visibilitychange', onFocus);
    return () => {
      window.removeEventListener('focus', onFocus);
      document.removeEventListener('visibilitychange', onFocus);
    };
  }, [load]);

  // Server's ActionResult tells us which items to drop. We animate the
  // card off first, then patch state so AnimatePresence can collapse.
  const onCommit = (item: StackItem, emoji: string, removedIDs: string[]) => {
    const key = itemKey(item);
    window.setTimeout(() => {
      setItems((cs) =>
        cs ? cs.filter((x) => !removedIDs.includes(itemKey(x)) && itemKey(x) !== key) : cs,
      );
      setBurst({ id: key, emoji });
      window.setTimeout(() => setBurst(null), 900);
    }, 260);
  };

  if (err) return <div className="empty">Error: {err}</div>;
  if (items === null) return <div className="empty">Loading…</div>;
  if (items.length === 0) {
    return (
      <main className="feed">
        <div className="empty">
          <div>Nothing needs you right now.</div>
        </div>
        <DegradedFooter degraded={degraded} />
      </main>
    );
  }

  return (
    <main className="feed" ref={feedRef} onScroll={onScroll}>
      <AnimatePresence initial={false}>
        {items.map((it) => (
          <motion.section
            key={itemKey(it)}
            className="card-screen"
            layout
            exit={{ height: 0, opacity: 0, transition: { duration: 0.25 } }}
          >
            <SwipeCard item={it} onCommit={onCommit} />
          </motion.section>
        ))}
      </AnimatePresence>
      <AnimatePresence>
        {burst && <Burst key={burst.id} emoji={burst.emoji} />}
      </AnimatePresence>
      <QueueIndicator count={items.length} progress={progress} />
      <DegradedFooter degraded={degraded} />
    </main>
  );
}

// QueueIndicator renders a thin bar fixed to the bottom of the viewport
// with a thumb sized to 1/count and positioned by scroll progress.
// Hidden for one-card (or empty) stacks — no value when there's nothing
// to navigate between.
function QueueIndicator({ count, progress }: { count: number; progress: number }) {
  if (count <= 1) return null;
  const thumbWidth = 100 / count;
  const thumbLeft = progress * (100 - thumbWidth);
  return (
    <div className="queue-indicator" aria-hidden>
      <div
        className="queue-indicator-thumb"
        style={{ width: `${thumbWidth}%`, left: `${thumbLeft}%` }}
      />
    </div>
  );
}

function DegradedFooter({ degraded }: { degraded: StackResponse['degraded'] }) {
  if (!degraded || degraded.length === 0) return null;
  return (
    <div className="degraded">
      {degraded.map((d) => (
        <span key={d.source_app} className="degraded-chip">
          {d.source_app} temporarily unavailable
        </span>
      ))}
    </div>
  );
}

function Burst({ emoji }: { emoji: string }) {
  return (
    <motion.div
      className="burst"
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0, transition: { duration: 0.25 } }}
    >
      <motion.span
        aria-hidden
        initial={{ scale: 0.5 }}
        animate={{ scale: 1.2 }}
        exit={{ scale: 1.6, transition: { duration: 0.35 } }}
        transition={{ type: 'spring', stiffness: 400, damping: 18 }}
      >
        {emoji}
      </motion.span>
    </motion.div>
  );
}

function findAction(actions: StackAction[], direction: CommitDirection): StackAction | undefined {
  return actions.find((a) => a.direction === direction);
}

function SwipeCard({
  item,
  onCommit,
}: {
  item: StackItem;
  onCommit: (item: StackItem, emoji: string, removedIDs: string[]) => void;
}) {
  const rightAction = findAction(item.actions, 'right');
  const leftAction = findAction(item.actions, 'left');
  const canSwipeLeft = !!leftAction;
  const canSwipeRight = !!rightAction;

  const x = useMotionValue(0);
  const threshold =
    typeof window !== 'undefined' ? Math.max(260, window.innerWidth * 0.7) : 260;
  const armedStart = threshold - 10;

  const rightOpacity = useTransform(x, [0, threshold * 0.4, threshold], [0, 0.6, 1]);
  const rightScale = useTransform(x, [armedStart, threshold], [1, 1.35]);
  const leftOpacity = useTransform(x, [-threshold, -threshold * 0.4, 0], [1, 0.6, 0]);
  const leftScale = useTransform(x, [-threshold, -armedStart], [1.35, 1]);

  const navigate = useNavigate();
  const [busy, setBusy] = useState(false);
  const [swipingOut, setSwipingOut] = useState<CommitDirection | null>(null);
  const [armed, setArmed] = useState<'right' | 'left' | null>(null);

  useMotionValueEvent(x, 'change', (v) => {
    if (canSwipeRight && v >= threshold) setArmed('right');
    else if (canSwipeLeft && v <= -threshold) setArmed('left');
    else setArmed(null);
  });

  const runAction = async (direction: CommitDirection, action: StackAction) => {
    setBusy(true);
    setSwipingOut(direction);
    try {
      const result = await api.doAction(
        item.source_app,
        item.kind,
        item.id,
        action.id,
        action.params,
      );
      onCommit(item, action.emoji, result.removed_ids ?? [itemKey(item)]);
    } catch (e) {
      setBusy(false);
      setSwipingOut(null);
      alert((e as Error).message);
    }
  };

  const onDragEnd = async (_e: unknown, info: PanInfo) => {
    if (busy) return;
    const snapBack = () =>
      animate(x, 0, { type: 'spring', stiffness: 500, damping: 32 });
    if (canSwipeRight && rightAction && info.offset.x > threshold) {
      await runAction('right', rightAction);
      return;
    }
    if (canSwipeLeft && leftAction && info.offset.x < -threshold) {
      await runAction('left', leftAction);
      return;
    }
    snapBack();
  };

  const renderer = rendererFor(item);
  const Face = renderer.Face;

  return (
    <motion.article
      className={`card tier-${item.priority_tier}${armed ? ` armed-${armed}` : ''}`}
      drag={busy ? false : 'x'}
      dragConstraints={{
        left: canSwipeLeft ? -500 : 0,
        right: canSwipeRight ? 500 : 0,
      }}
      dragElastic={0.3}
      style={{ x }}
      animate={
        swipingOut === 'right'
          ? { x: 520, opacity: 0, transition: { duration: 0.25 } }
          : swipingOut === 'left'
            ? { x: -520, opacity: 0, transition: { duration: 0.25 } }
            : undefined
      }
      onDragEnd={onDragEnd}
      onClick={() => {
        if (busy) return;
        navigate(`/stack/${item.source_app}/${item.kind}/${item.id}`);
      }}
    >
      <div className="kind-tag">
        {item.icon ? `${item.icon} ` : ''}
        {item.kind_label}
      </div>
      {item.badges && item.badges.length > 0 && (
        <div className="badges">
          {item.badges.map((b, i) => (
            <span key={i} className={`badge tone-${b.tone}`}>
              {b.label}
            </span>
          ))}
        </div>
      )}
      <h2>{item.title}</h2>
      {item.body && (
        <div className="body markdown">
          <ReactMarkdown remarkPlugins={[remarkGfm]}>{item.body}</ReactMarkdown>
        </div>
      )}
      {Face && <Face item={item} />}
      {canSwipeRight && rightAction && (
        <motion.div
          className="swipe-hint right"
          style={{ opacity: rightOpacity, scale: rightScale }}
        >
          {rightAction.emoji} {rightAction.label}
        </motion.div>
      )}
      {canSwipeLeft && leftAction && (
        <motion.div
          className="swipe-hint left"
          style={{ opacity: leftOpacity, scale: leftScale }}
        >
          {leftAction.emoji}
        </motion.div>
      )}
    </motion.article>
  );
}
