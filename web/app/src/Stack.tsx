import { forwardRef, useCallback, useEffect, useImperativeHandle, useRef, useState } from 'react';
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
import CardChatSheet from './chat/CardChatSheet';

// LONG_PRESS_MS is how long a pointer must be held on a card before we
// open the chat sheet. Chosen so accidental slow taps don't trigger it
// but an intentional "press and hold" still feels snappy.
const LONG_PRESS_MS = 600;

type CommitDirection = 'right' | 'left';

export default function Stack() {
  const [items, setItems] = useState<StackItem[] | null>(null);
  const [degraded, setDegraded] = useState<StackResponse['degraded']>([]);
  const [err, setErr] = useState<string | null>(null);
  const [burst, setBurst] = useState<{ id: string; emoji: string } | null>(null);
  const [progress, setProgress] = useState(0);
  const [chatItem, setChatItem] = useState<StackItem | null>(null);
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
      // location.hash (set by Slack deep-links) asks the backend to
      // hoist that itemKey to the top of the page, so we render the
      // target card at index 0 without pulling hundreds of rows and
      // then scrolling client-side.
      const focus = window.location.hash.replace(/^#/, '') || undefined;
      const resp = await api.stack({ focus });
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


  // showBurst pops the big checkmark overlay. Called the moment the
  // commit decision is made (start of runAction) so the feedback lands
  // in sync with the exit animation instead of after the API round
  // trip — otherwise a slow send leaves the user staring at an empty
  // screen before the burst catches up.
  const showBurst = (item: StackItem, emoji: string) => {
    setBurst({ id: itemKey(item), emoji });
    window.setTimeout(() => setBurst(null), 900);
  };

  // Server's ActionResult tells us which items to drop. We animate the
  // card off first, then patch state so AnimatePresence can collapse.
  const onCommit = (item: StackItem, _emoji: string, removedIDs: string[]) => {
    const key = itemKey(item);
    window.setTimeout(() => {
      setItems((cs) =>
        cs ? cs.filter((x) => !removedIDs.includes(itemKey(x)) && itemKey(x) !== key) : cs,
      );
    }, 260);
  };

  // Keyboard shortcuts for desktop testing. Hold-to-commit: pressing
  // an arrow key starts pushing the top card across at a constant
  // speed; releasing before the threshold snaps it back, holding for
  // the full 2s commits the action. Mirrors the feel of a real swipe
  // (reviewable + cancelable) rather than an instant action.
  //   ArrowRight (hold 2s) → commit the right swipe action
  //   ArrowLeft  (hold 2s) → commit the left swipe action
  //   Enter                → open the detail view
  const navigate = useNavigate();
  const topCardRef = useRef<SwipeCardHandle | null>(null);
  useEffect(() => {
    if (!items || items.length === 0 || chatItem) return;
    const onKeyDown = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement | null;
      if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)) {
        return;
      }
      const active = items[0];
      if (!active) return;
      if (e.key === 'Enter' && !e.repeat) {
        e.preventDefault();
        navigate(`/stack/${active.source_app}/${active.kind}/${active.id}`);
        return;
      }
      if (e.repeat) return; // ignore OS auto-repeat while held
      if (e.key === 'ArrowRight') {
        e.preventDefault();
        topCardRef.current?.startSwipe('right');
      } else if (e.key === 'ArrowLeft') {
        e.preventDefault();
        topCardRef.current?.startSwipe('left');
      }
    };
    const onKeyUp = (e: KeyboardEvent) => {
      if (e.key === 'ArrowRight' || e.key === 'ArrowLeft') {
        topCardRef.current?.cancelSwipe();
      }
    };
    window.addEventListener('keydown', onKeyDown);
    window.addEventListener('keyup', onKeyUp);
    return () => {
      window.removeEventListener('keydown', onKeyDown);
      window.removeEventListener('keyup', onKeyUp);
    };
  }, [items, chatItem, navigate]);

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
      <AnimatePresence initial={false} mode="popLayout">
        {items.map((it, idx) => (
          <motion.section
            key={itemKey(it)}
            className="card-screen"
            layout
            exit={{ height: 0, opacity: 0, transition: { duration: 0.25 } }}
          >
            <SwipeCard
              ref={idx === 0 ? topCardRef : null}
              item={it}
              onCommit={onCommit}
              onShowBurst={showBurst}
              onLongPress={setChatItem}
              disableLongPress={chatItem !== null}
            />
          </motion.section>
        ))}
      </AnimatePresence>
      <AnimatePresence>
        {burst && <Burst key={burst.id} emoji={burst.emoji} />}
      </AnimatePresence>
      <QueueIndicator count={items.length} progress={progress} />
      <DegradedFooter degraded={degraded} />
      {chatItem && (
        <CardChatSheet
          sourceApp={chatItem.source_app}
          kind={chatItem.kind}
          id={chatItem.id}
          title={chatItem.title}
          onClose={() => {
            setChatItem(null);
            load();
          }}
          onTurnDone={load}
        />
      )}
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

type SwipeCardHandle = {
  // startSwipe begins the keyboard hold-to-commit animation. x
  // advances toward the commit threshold at a constant linear rate;
  // completing triggers the same runAction path a real swipe uses.
  startSwipe: (direction: CommitDirection) => void;
  // cancelSwipe aborts an in-flight keyboard push and snaps the card
  // back to resting. No-op after runAction has already fired.
  cancelSwipe: () => void;
};

// HOLD_TO_COMMIT_S sets how long a user must hold an arrow key for
// the card to cross its swipe threshold. Short enough to feel
// responsive, long enough to bail out if you reconsider mid-push.
const HOLD_TO_COMMIT_S = 2;

type SwipeCardProps = {
  item: StackItem;
  onCommit: (item: StackItem, emoji: string, removedIDs: string[]) => void;
  onShowBurst: (item: StackItem, emoji: string) => void;
  onLongPress: (item: StackItem) => void;
  disableLongPress: boolean;
};

const SwipeCard = forwardRef<SwipeCardHandle, SwipeCardProps>(function SwipeCard(
  { item, onCommit, onShowBurst, onLongPress, disableLongPress }: SwipeCardProps,
  ref,
) {
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
  // Synchronous guard against double-dispatch. setBusy is async so two
  // tightly-spaced inputs (finger release + keyboard keydown, or two
  // fast swipes) could both see busy=false and fire runAction twice,
  // landing the second resolve on an already-resolving card and
  // surfacing "card is not pending".
  const runningRef = useRef(false);
  // Long-press timer. Fires LONG_PRESS_MS after pointerdown if the
  // pointer hasn't moved (dragging clears it) and no other card is
  // already showing a chat sheet. Setting justOpened suppresses the
  // click-to-navigate that would otherwise fire on release.
  const longPressTimerRef = useRef<number | null>(null);
  const longPressFiredRef = useRef(false);
  // Set when Framer detects a drag for this gesture. onTap can still fire
  // after a small drag-and-back release, which would wrong-navigate to the
  // detail view. Cleared on the next interaction.
  const wasDraggedRef = useRef(false);

  const clearLongPress = () => {
    if (longPressTimerRef.current !== null) {
      window.clearTimeout(longPressTimerRef.current);
      longPressTimerRef.current = null;
    }
  };

  useMotionValueEvent(x, 'change', (v) => {
    if (canSwipeRight && v >= threshold) setArmed('right');
    else if (canSwipeLeft && v <= -threshold) setArmed('left');
    else setArmed(null);
  });

  const runAction = async (direction: CommitDirection, action: StackAction) => {
    if (runningRef.current) return;
    runningRef.current = true;
    setBusy(true);
    setSwipingOut(direction);
    // Burst fires immediately so it lands with the exit animation
    // regardless of how long the server takes; item removal still
    // waits for the server to confirm (and to tell us what to drop).
    onShowBurst(item, action.emoji);
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
      runningRef.current = false;
      setBusy(false);
      setSwipingOut(null);
      alert((e as Error).message);
    }
  };

  // pushAnimRef holds the in-flight hold-to-commit animation so a key
  // release can stop it before the threshold is crossed.
  const pushAnimRef = useRef<ReturnType<typeof animate> | null>(null);

  // Imperative handle so Stack's keyboard shortcuts can drive the
  // same motion-value path a real swipe uses. Pressing an arrow key
  // slides the card toward the commit threshold at a constant speed;
  // releasing before completion snaps it back.
  useImperativeHandle(
    ref,
    () => ({
      startSwipe: (direction) => {
        if (runningRef.current) return;
        const action = direction === 'right' ? rightAction : leftAction;
        if (!action) return;
        if (pushAnimRef.current) pushAnimRef.current.stop();
        const target = direction === 'right' ? threshold + 20 : -(threshold + 20);
        pushAnimRef.current = animate(x, target, {
          duration: HOLD_TO_COMMIT_S,
          ease: 'linear',
          onComplete: () => {
            pushAnimRef.current = null;
            runAction(direction, action);
          },
        });
      },
      cancelSwipe: () => {
        if (!pushAnimRef.current) return;
        pushAnimRef.current.stop();
        pushAnimRef.current = null;
        if (runningRef.current) return;
        animate(x, 0, { type: 'spring', stiffness: 500, damping: 32 });
      },
    }),
    [rightAction, leftAction, threshold, x],
  );

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
    <div className="swipe-card-shell">
      {canSwipeRight && rightAction && (
        <div className="swipe-hint-wrap">
          <motion.div
            className="swipe-hint right"
            style={{ opacity: rightOpacity, scale: rightScale }}
          >
            {rightAction.emoji} {rightAction.label}
          </motion.div>
        </div>
      )}
      {canSwipeLeft && leftAction && (
        <div className="swipe-hint-wrap">
          <motion.div
            className="swipe-hint left"
            style={{ opacity: leftOpacity, scale: leftScale }}
          >
            {leftAction.emoji} {leftAction.label}
          </motion.div>
        </div>
      )}
      <motion.article
      className={`card tier-${item.priority_tier}${armed ? ` armed-${armed}` : ''}`}
      drag={busy ? false : 'x'}
      dragConstraints={{
        left: canSwipeLeft ? -500 : 0,
        right: canSwipeRight ? 500 : 0,
      }}
      dragElastic={0.3}
      style={{
        x,
        // Suppress iOS Safari's long-press context/share menu so our
        // own long-press gesture isn't hijacked.
        WebkitTouchCallout: 'none' as const,
        WebkitUserSelect: 'none' as const,
      }}
      animate={
        swipingOut === 'right'
          ? { x: 520, opacity: 0, transition: { duration: 0.25 } }
          : swipingOut === 'left'
            ? { x: -520, opacity: 0, transition: { duration: 0.25 } }
            : undefined
      }
      onTapStart={() => {
        if (disableLongPress || busy) return;
        wasDraggedRef.current = false;
        longPressFiredRef.current = false;
        longPressTimerRef.current = window.setTimeout(() => {
          longPressFiredRef.current = true;
          longPressTimerRef.current = null;
          onLongPress(item);
        }, LONG_PRESS_MS);
      }}
      // onTapCancel fires when the pointer moves far enough that a tap
      // can't complete — e.g. during vertical scroll. Without this the
      // timer ticks to completion and opens chat on whatever card the
      // scroll started on.
      onTapCancel={clearLongPress}
      onDragStart={() => {
        clearLongPress();
        wasDraggedRef.current = true;
      }}
      onDragEnd={async (e, info) => {
        clearLongPress();
        await onDragEnd(e, info);
      }}
      onTap={() => {
        clearLongPress();
        if (busy) return;
        // A drag-and-release (even small, where the card snaps back) must
        // not fall through to the detail-view navigation — only a clean
        // tap with no drag opens the detail page.
        if (wasDraggedRef.current) {
          wasDraggedRef.current = false;
          return;
        }
        if (longPressFiredRef.current) {
          longPressFiredRef.current = false;
          return;
        }
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
      </motion.article>
    </div>
  );
});
