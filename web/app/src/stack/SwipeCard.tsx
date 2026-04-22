import { forwardRef, useImperativeHandle, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  animate,
  motion,
  useMotionValue,
  useMotionValueEvent,
  useTransform,
  type PanInfo,
} from 'framer-motion';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { api } from '../api';
import type { StackAction, StackItem } from '../types';
import { itemKey } from '../types';
import { rendererFor } from '../kinds';

export type CommitDirection = 'right' | 'left';

// LONG_PRESS_MS is how long a pointer must be held on a card before we
// open the chat sheet. Chosen so accidental slow taps don't trigger it
// but an intentional "press and hold" still feels snappy.
const LONG_PRESS_MS = 600;

// HOLD_TO_COMMIT_S sets how long a user must hold an arrow key for
// the card to cross its swipe threshold. Short enough to feel
// responsive, long enough to bail out if you reconsider mid-push.
const HOLD_TO_COMMIT_S = 2;

export type SwipeCardHandle = {
  // startSwipe begins the keyboard hold-to-commit animation. x
  // advances toward the commit threshold at a constant linear rate;
  // completing triggers the same runAction path a real swipe uses.
  startSwipe: (direction: CommitDirection) => void;
  // cancelSwipe aborts an in-flight keyboard push and snaps the card
  // back to resting. No-op after runAction has already fired.
  cancelSwipe: () => void;
};

type SwipeCardProps = {
  item: StackItem;
  onCommit: (item: StackItem, emoji: string, removedIDs: string[]) => void;
  onShowBurst: (item: StackItem, emoji: string) => void;
  onLongPress: (item: StackItem) => void;
  disableLongPress: boolean;
};

function findAction(actions: StackAction[], direction: CommitDirection): StackAction | undefined {
  return actions.find((a) => a.direction === direction);
}

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

export default SwipeCard;
