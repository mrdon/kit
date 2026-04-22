import { useCallback, useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { AnimatePresence, motion } from 'framer-motion';
import { api } from './api';
import type { StackItem, StackResponse } from './types';
import { itemKey } from './types';
import CardChatSheet from './chat/CardChatSheet';
import QuickChatSheet from './chat/QuickChatSheet';
import SwipeCard, { type SwipeCardHandle } from './stack/SwipeCard';

export default function Stack() {
  const [items, setItems] = useState<StackItem[] | null>(null);
  const [degraded, setDegraded] = useState<StackResponse['degraded']>([]);
  const [err, setErr] = useState<string | null>(null);
  const [burst, setBurst] = useState<{ id: string; emoji: string } | null>(null);
  const [progress, setProgress] = useState(0);
  const [chatItem, setChatItem] = useState<StackItem | null>(null);
  const [quickChatOpen, setQuickChatOpen] = useState(false);
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
  if (items.length === 0) {
    return (
      <main className="feed">
        <div className="empty">
          <div>Nothing needs you right now.</div>
        </div>
        <DegradedFooter degraded={degraded} />
        <QuickChatFab onClick={() => setQuickChatOpen(true)} />
        {quickChatOpen && (
          <QuickChatSheet onClose={() => setQuickChatOpen(false)} onTurnDone={load} />
        )}
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
              disableLongPress={sheetOpen}
            />
          </motion.section>
        ))}
      </AnimatePresence>
      <AnimatePresence>
        {burst && <Burst key={burst.id} emoji={burst.emoji} />}
      </AnimatePresence>
      <QueueIndicator count={items.length} progress={progress} />
      <DegradedFooter degraded={degraded} />
      <QuickChatFab onClick={() => setQuickChatOpen(true)} />
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

// Floating action button anchored bottom-right of the feed. Tap opens
// the QuickChatSheet for fast capture (todos, decisions, notes) or a
// quick question. Sits above the card swipe area and uses
// touch-action: manipulation to keep taps snappy without stealing
// horizontal drags from the top card.
function QuickChatFab({ onClick }: { onClick: () => void }) {
  return (
    <button
      type="button"
      className="quick-chat-fab"
      aria-label="Quick chat"
      onClick={onClick}
    >
      +
    </button>
  );
}
