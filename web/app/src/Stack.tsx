import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { AnimatePresence, motion } from 'framer-motion';
import { api, stackActionUrl } from './api';
import type { StackAction, StackItem, StackResponse } from './types';
import { itemKey } from './types';
import CardChatSheet from './chat/CardChatSheet';
import QuickChatSheet from './chat/QuickChatSheet';
import {
  isAudioCaptureSupported,
  startAudioCapture,
  type AudioCaptureSession,
} from './chat/audioCapture';
import SwipeCard, { type SwipeCardHandle } from './stack/SwipeCard';
import { showToast } from './toast/bus';

// UNDO_FUSE_MS — how long a committed swipe stays pending (and
// undoable via the toast) before the POST actually fires. Mirrors
// Gmail's "Undo Send" window. If a new action arrives before this
// elapses, the previous one commits early (toast is single-slot).
const UNDO_FUSE_MS = 5000;

// COMMIT_ANIMATION_MS — how long the swipe-out animation runs before
// we filter the card out of the list. Matches the old removal delay.
const COMMIT_ANIMATION_MS = 260;

// TOP_KEY_STORAGE — sessionStorage key for the itemKey of the card
// currently at the top of the viewport. Restored after list mutations
// (mount after navigation, resolve) so the user doesn't lose their
// place: (1) back from the detail route lands on the same card, and
// (2) resolving the last scrolled-to card advances to the next card
// instead of letting scrollTop clamp back to one already seen.
const TOP_KEY_STORAGE = 'kit:stack:topKey';

type PendingAction = {
  id: number;
  item: StackItem;
  action: StackAction;
  originalIndex: number;
};

export default function Stack() {
  const [items, setItems] = useState<StackItem[] | null>(null);
  const [degraded, setDegraded] = useState<StackResponse['degraded']>([]);
  const [err, setErr] = useState<string | null>(null);
  const [burst, setBurst] = useState<{ id: string; emoji: string } | null>(null);
  const [progress, setProgress] = useState(0);
  const [chatItem, setChatItem] = useState<StackItem | null>(null);
  const [quickChatOpen, setQuickChatOpen] = useState(false);
  // Audio captured by a long-press on the FAB. Handed to QuickChatSheet
  // on open so its composer transcribes it into the textarea.
  const [quickChatSeedBlob, setQuickChatSeedBlob] = useState<Blob | null>(null);
  const feedRef = useRef<HTMLElement | null>(null);

  // itemsRef mirrors items so onScroll can read the current list
  // without re-binding (it fires faster than state closures refresh).
  const itemsRef = useRef<StackItem[] | null>(null);
  useEffect(() => {
    itemsRef.current = items;
  }, [items]);

  const onScroll = useCallback(() => {
    const el = feedRef.current;
    if (!el) return;
    const max = el.scrollHeight - el.clientHeight;
    setProgress(max > 0 ? el.scrollTop / max : 0);
    // Remember the top-visible card so we can restore it across
    // remounts (back-nav from detail) and list mutations (resolve).
    const vh = el.clientHeight;
    const list = itemsRef.current;
    if (vh > 0 && list && list.length > 0) {
      const idx = Math.min(list.length - 1, Math.max(0, Math.round(el.scrollTop / vh)));
      try {
        sessionStorage.setItem(TOP_KEY_STORAGE, itemKey(list[idx]));
      } catch {
        // sessionStorage can throw in private-mode Safari; the scroll
        // restore is a nice-to-have so we just skip it.
      }
    }
  }, []);

  // Keep progress in sync when the item list changes (completions shrink
  // the scroll height; without a recompute the thumb drifts stale).
  useEffect(() => {
    onScroll();
  }, [items, onScroll]);

  // Restore scroll to the remembered top card after any list mutation.
  // Runs in a layout effect so the jump happens before paint (no flicker
  // back to the top). Skips when nothing is saved or the saved card is
  // gone from the current list — in that case the natural scroll
  // position stands.
  useLayoutEffect(() => {
    if (!items || items.length === 0) return;
    const el = feedRef.current;
    if (!el) return;
    let saved: string | null = null;
    try {
      saved = sessionStorage.getItem(TOP_KEY_STORAGE);
    } catch {
      return;
    }
    if (!saved) return;
    const idx = items.findIndex((i) => itemKey(i) === saved);
    if (idx < 0) return;
    const target = idx * el.clientHeight;
    if (Math.abs(el.scrollTop - target) > 1) {
      el.scrollTo({ top: target, behavior: 'auto' });
    }
  }, [items]);

  const load = useCallback(async () => {
    try {
      // location.hash (set by Slack deep-links) asks the backend to
      // hoist that itemKey to the top of the page, so we render the
      // target card at index 0 without pulling hundreds of rows and
      // then scrolling client-side. Consume it once — the server's
      // focus path calls GetItem which bypasses feed filters like
      // snooze, so a stale hash would keep re-hoisting a snoozed card
      // on every refetch (visibility change, chat close, back-nav).
      const focus = window.location.hash.replace(/^#/, '') || undefined;
      if (focus) {
        window.history.replaceState(
          null,
          '',
          window.location.pathname + window.location.search,
        );
      }
      const resp = await api.stack({ focus });
      // Filter out items that are pending an undo-fuse commit — the
      // server still has them in the stack (the POST hasn't fired yet),
      // but the user has visually swiped them off. Let them stay off.
      const pendingKeys = new Set(
        Array.from(pendingRef.current.values()).map((pa) => itemKey(pa.item)),
      );
      const items = pendingKeys.size
        ? (resp.items ?? []).filter((x) => !pendingKeys.has(itemKey(x)))
        : (resp.items ?? []);
      setItems(items);
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

  // Pending swipe actions awaiting their undo-fuse. Keyed by pending id.
  // We keep this in a ref so the pagehide flush and async commit can
  // read/write without closure staleness.
  const pendingRef = useRef<Map<number, PendingAction>>(new Map());
  const pendingCounterRef = useRef(0);

  // commitPending fires the actual POST after the undo fuse expires.
  // On success, drop any additional items the server flagged (decision
  // resolves can close linked cards). On failure, restore the item and
  // show an error toast — the card pops back to the top so the user
  // can retry.
  const commitPending = useCallback(async (pa: PendingAction) => {
    try {
      const result = await api.doAction(
        pa.item.source_app,
        pa.item.kind,
        pa.item.id,
        pa.action.id,
        pa.action.params,
      );
      const removed = result.removed_ids ?? [];
      if (removed.length > 0) {
        setItems((cs) => (cs ? cs.filter((x) => !removed.includes(itemKey(x))) : cs));
      }
    } catch (e) {
      const key = itemKey(pa.item);
      try {
        sessionStorage.setItem(TOP_KEY_STORAGE, key);
      } catch {
        // ignore
      }
      setItems((cs) => {
        if (!cs) return cs;
        if (cs.some((x) => itemKey(x) === key)) return cs;
        const idx = Math.min(pa.originalIndex, cs.length);
        return [...cs.slice(0, idx), pa.item, ...cs.slice(idx)];
      });
      showToast({
        kind: 'error',
        message: (e as Error).message || 'Action failed',
        duration: 6000,
      });
    }
  }, []);

  // onCommit is called by SwipeCard the moment the user completes a
  // swipe. We wait COMMIT_ANIMATION_MS so the card finishes animating
  // off, then remove it from the list, queue a PendingAction, and show
  // the undo toast. The real POST only fires when the toast expires or
  // gets replaced by a newer swipe.
  const onCommit = useCallback(
    (item: StackItem, action: StackAction) => {
      const key = itemKey(item);
      const originalIndex = items?.findIndex((x) => itemKey(x) === key) ?? 0;
      const pendingId = ++pendingCounterRef.current;
      // After removal, the user should advance to what was below, not
      // clamp back to what they already scrolled past. Point the saved
      // top key at the next card (or the previous one if this was the
      // last) so the restore effect places us there.
      const neighbor =
        (items && items[originalIndex + 1]) ||
        (items && items[originalIndex - 1]) ||
        null;
      if (neighbor) {
        try {
          sessionStorage.setItem(TOP_KEY_STORAGE, itemKey(neighbor));
        } catch {
          // ignore — see onScroll for rationale
        }
      }

      window.setTimeout(() => {
        setItems((cs) => (cs ? cs.filter((x) => itemKey(x) !== key) : cs));

        const pa: PendingAction = { id: pendingId, item, action, originalIndex };
        pendingRef.current.set(pendingId, pa);

        showToast({
          kind: 'pending',
          message: `${action.emoji} ${action.label}`,
          action: {
            label: 'Undo',
            onClick: () => {
              pendingRef.current.delete(pendingId);
              // Put the saved top key back on the undone card so the
              // restore effect doesn't bounce us past it to the neighbor
              // we set during commit.
              try {
                sessionStorage.setItem(TOP_KEY_STORAGE, key);
              } catch {
                // ignore
              }
              setItems((cs) => {
                if (!cs) return cs;
                if (cs.some((x) => itemKey(x) === key)) return cs;
                const idx = Math.min(originalIndex, cs.length);
                return [...cs.slice(0, idx), item, ...cs.slice(idx)];
              });
            },
          },
          duration: UNDO_FUSE_MS,
          onExpire: () => {
            const p = pendingRef.current.get(pendingId);
            if (!p) return;
            pendingRef.current.delete(pendingId);
            void commitPending(p);
          },
        });
      }, COMMIT_ANIMATION_MS);
    },
    [items, commitPending],
  );

  // Flush any pending actions via sendBeacon when the page is about to
  // go away. pagehide fires on real navigation/close; visibilitychange
  // covers mobile app-background. sendBeacon is reliable in both.
  useEffect(() => {
    const flush = () => {
      if (pendingRef.current.size === 0) return;
      for (const pa of Array.from(pendingRef.current.values())) {
        const body = JSON.stringify({
          action_id: pa.action.id,
          params: pa.action.params ?? undefined,
        });
        navigator.sendBeacon(
          stackActionUrl(pa.item.source_app, pa.item.kind, pa.item.id),
          new Blob([body], { type: 'application/json' }),
        );
      }
      pendingRef.current.clear();
    };
    const onVisibility = () => {
      if (document.visibilityState === 'hidden') flush();
    };
    window.addEventListener('pagehide', flush);
    document.addEventListener('visibilitychange', onVisibility);
    return () => {
      window.removeEventListener('pagehide', flush);
      document.removeEventListener('visibilitychange', onVisibility);
      // SPA route change (e.g. tapping into a card's detail view)
      // unmounts Stack without firing pagehide or visibilitychange —
      // without this flush, any in-flight undo-fuse snooze/complete
      // silently dies with the component and the server never hears
      // about it. The snoozed card then reappears on remount.
      flush();
    };
  }, []);

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
  const sheetOpen = chatItem !== null || quickChatOpen;
  useEffect(() => {
    if (!items || items.length === 0 || sheetOpen) return;
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
  }, [items, sheetOpen, navigate]);

  if (err) return <div className="empty">Error: {err}</div>;
  if (items === null) return <div className="empty">Loading…</div>;

  // One unified render so the chat/quick-chat sheets sit at a stable
  // tree position across items=[] ↔ items.length>0 transitions.
  // Reconciling on stable positions keeps the sheet's turn state and
  // the auto-dismiss timer alive when a capture creates the first card.
  const empty = items.length === 0;

  return (
    <main className="feed" ref={empty ? null : feedRef} onScroll={empty ? undefined : onScroll}>
      {empty ? (
        <div className="empty">
          <div>Nothing needs you right now.</div>
        </div>
      ) : (
        <>
          <AnimatePresence initial={false} mode="popLayout">
            {items.map((it, idx) => (
              <motion.section
                key={itemKey(it)}
                className="card-screen"
                exit={{ height: 0, opacity: 0, transition: { duration: 0.25 } }}
              >
                <SwipeCard
                  ref={idx === 0 ? topCardRef : null}
                  item={it}
                  onCommit={onCommit}
                  onShowBurst={showBurst}
                  onLongPress={setChatItem}
                  disableLongPress={sheetOpen}
                />
              </motion.section>
            ))}
          </AnimatePresence>
          <AnimatePresence>
            {burst && <Burst key={burst.id} emoji={burst.emoji} />}
          </AnimatePresence>
          <QueueIndicator count={items.length} progress={progress} />
        </>
      )}
      <DegradedFooter degraded={degraded} />
      <QuickChatFab
        onTap={() => {
          setQuickChatSeedBlob(null);
          setQuickChatOpen(true);
        }}
        onRecordingStop={(blob) => {
          setQuickChatSeedBlob(blob);
          setQuickChatOpen(true);
        }}
      />
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
      {quickChatOpen && (
        <QuickChatSheet
          onClose={() => {
            setQuickChatOpen(false);
            setQuickChatSeedBlob(null);
            load();
          }}
          onTurnDone={load}
          seedAudioBlob={quickChatSeedBlob}
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

// LONG_PRESS_MS — how long the FAB must be held before it switches
// from "open chat on release" to "arm voice recording". Long enough
// that an accidental finger-rest doesn't start recording, short enough
// that an intentional press feels responsive.
const LONG_PRESS_MS = 600;

// Floating action button anchored bottom-right of the feed.
//   - Quick tap: opens QuickChatSheet for typed/mic capture.
//   - Long press (held past LONG_PRESS_MS): arms and starts recording
//     in place, FAB turns red. Releasing the finger keeps recording.
//   - Tap while recording: stops, opens QuickChatSheet seeded with the
//     captured audio blob so the composer transcribes it into the
//     textarea.
//
// Audio capture is shared with the chat composer's mic via
// audioCapture.ts so MIME selection and stream cleanup don't drift.
// DISARM_ANIM_MS — how long the post-tap "stopping" pulse plays before
// the chat sheet opens. Without this beat, the FAB switches color and
// the sheet appears in the same frame, which reads as glitchy / "did
// I miss the tap?". A short check-mark flash makes the stop feel
// deliberate.
const DISARM_ANIM_MS = 320;

function QuickChatFab({
  onTap,
  onRecordingStop,
}: {
  onTap: () => void;
  onRecordingStop: (blob: Blob) => void;
}) {
  const [recording, setRecording] = useState(false);
  const [disarming, setDisarming] = useState(false);
  const sessionRef = useRef<AudioCaptureSession | null>(null);
  const armTimerRef = useRef<number | null>(null);
  // True once the long-press timer fires within a single press; lets
  // pointerup distinguish "quick tap → open chat" from "long press →
  // started recording, leave it running".
  const armedThisPressRef = useRef(false);
  const supported = isAudioCaptureSupported();

  const cancelArming = () => {
    if (armTimerRef.current !== null) {
      window.clearTimeout(armTimerRef.current);
      armTimerRef.current = null;
    }
  };

  useEffect(() => {
    return () => {
      cancelArming();
      sessionRef.current?.cancel();
      sessionRef.current = null;
    };
  }, []);

  const startRecording = async () => {
    try {
      sessionRef.current = await startAudioCapture();
      setRecording(true);
      // Brief haptic so the user knows recording started — they may
      // already be lifting their finger by the time the timer fires.
      try {
        navigator.vibrate?.(30);
      } catch {
        // ignore — vibrate is best-effort
      }
    } catch {
      // Permission denied or device unavailable. Fall back to opening
      // the chat sheet so the user can type instead.
      sessionRef.current = null;
      onTap();
    }
  };

  const stopRecordingAndOpen = async () => {
    const session = sessionRef.current;
    sessionRef.current = null;
    setRecording(false);
    setDisarming(true);
    // Confirm haptic — best-effort, no-op on iOS.
    try {
      navigator.vibrate?.(20);
    } catch {
      // ignore
    }
    if (!session) {
      window.setTimeout(() => {
        setDisarming(false);
        onTap();
      }, DISARM_ANIM_MS);
      return;
    }
    // Run the stop and the pulse animation in parallel; whichever takes
    // longer drives the open. The animation gives the user a beat of
    // feedback even when stop() resolves nearly instantly.
    const [blob] = await Promise.all([
      session.stop(),
      new Promise((r) => window.setTimeout(r, DISARM_ANIM_MS)),
    ]);
    setDisarming(false);
    if (blob.size === 0) {
      onTap();
      return;
    }
    onRecordingStop(blob);
  };

  const onPointerDown = (e: React.PointerEvent) => {
    if (recording) {
      // Tap-to-stop: a fresh press while recording stops and opens.
      e.preventDefault();
      void stopRecordingAndOpen();
      return;
    }
    if (!supported) {
      // No MediaRecorder support — fall through to the click handler
      // for plain tap-to-open behavior.
      return;
    }
    // Capture the pointer so subsequent events (incl. pointerup) come
    // to us regardless of where the user's finger has drifted, and so
    // the browser doesn't later fire pointercancel to take over for
    // its own long-press gestures (text selection, context menu).
    try {
      e.currentTarget.setPointerCapture(e.pointerId);
    } catch {
      // older browsers — pointer events still flow, just without capture
    }
    e.preventDefault();
    armedThisPressRef.current = false;
    cancelArming();
    armTimerRef.current = window.setTimeout(() => {
      armTimerRef.current = null;
      armedThisPressRef.current = true;
      void startRecording();
    }, LONG_PRESS_MS);
  };

  const onPointerUp = (e: React.PointerEvent) => {
    try {
      e.currentTarget.releasePointerCapture(e.pointerId);
    } catch {
      // fine — capture may not have been set
    }
    if (recording) {
      // The long-press fired and we're now recording; finger release
      // does NOT stop. The user must tap again to stop.
      return;
    }
    if (armTimerRef.current !== null) {
      // Released before arming threshold → quick tap, open chat.
      cancelArming();
      if (!armedThisPressRef.current) onTap();
    }
  };

  const onPointerCancel = (e: React.PointerEvent) => {
    try {
      e.currentTarget.releasePointerCapture(e.pointerId);
    } catch {
      // fine
    }
    // pointercancel can fire even with pointer capture (e.g. an OS-level
    // interruption like a system alert). Cancel arming so a stale timer
    // doesn't surprise-record later, but don't kill an active recording.
    if (!recording) cancelArming();
  };

  return (
    <button
      type="button"
      className={`quick-chat-fab${recording ? ' recording' : ''}${disarming ? ' disarming' : ''}`}
      aria-label={
        recording
          ? 'Tap to stop recording'
          : supported
            ? 'Quick chat (hold to record)'
            : 'Quick chat'
      }
      onPointerDown={onPointerDown}
      onPointerUp={onPointerUp}
      onPointerCancel={onPointerCancel}
      onContextMenu={(e) => e.preventDefault()}
      onClick={() => {
        // Click fires only when the browser doesn't synthesize pointer
        // events (very rare) or when recording isn't supported and
        // pointerdown is a no-op. In the supported path, onTap is
        // already triggered via pointerup.
        if (!supported && !recording) onTap();
      }}
    >
      {disarming ? '✓' : recording ? '●' : '+'}
    </button>
  );
}
