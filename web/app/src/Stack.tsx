import { useCallback, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { AnimatePresence, motion, useMotionValue, useTransform, type PanInfo } from 'framer-motion';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { api } from './api';
import type { Card } from './types';

type CommitDirection = 'right' | 'left';

export default function Stack() {
  const [cards, setCards] = useState<Card[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [burst, setBurst] = useState<{ id: string; kind: 'up' | 'down' | 'approve' } | null>(null);

  const load = useCallback(async () => {
    try {
      const items = await api.stack();
      setCards(items);
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

  const onCommit = (c: Card, direction: CommitDirection) => {
    // Fire the burst first so the user sees the confirmation *on top of*
    // the still-present card while the exit animation runs.
    const burstKind: 'up' | 'down' | 'approve' =
      c.kind === 'decision' ? 'approve' : direction === 'right' ? 'up' : 'down';
    setBurst({ id: c.id, kind: burstKind });
    setCards((cs) => (cs ? cs.filter((x) => x.id !== c.id) : cs));
    setTimeout(() => setBurst(null), 900);
  };

  if (err) return <div className="empty">Error: {err}</div>;
  if (cards === null) return <div className="empty">Loading…</div>;
  if (cards.length === 0) {
    return <div className="empty"><div>Nothing needs you right now.</div></div>;
  }

  return (
    <main className="feed">
      <AnimatePresence initial={false}>
        {cards.map((c) => (
          <motion.section
            key={c.id}
            className="card-screen"
            layout
            exit={{ height: 0, opacity: 0, transition: { duration: 0.25 } }}
          >
            <SwipeCard card={c} onCommit={onCommit} />
          </motion.section>
        ))}
      </AnimatePresence>
      <AnimatePresence>
        {burst && <Burst key={burst.id} kind={burst.kind} />}
      </AnimatePresence>
    </main>
  );
}

function Burst({ kind }: { kind: 'up' | 'down' | 'approve' }) {
  const emoji = kind === 'up' ? '👍' : kind === 'down' ? '👎' : '✅';
  // Outer div stays a pure flex centerer; the inner motion.span owns the
  // animated transform. Putting the scale on the outer element replaces
  // the flex centering and pushes the emoji off-screen to the right.
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

function SwipeCard({ card, onCommit }: { card: Card; onCommit: (c: Card, direction: CommitDirection) => void }) {
  const x = useMotionValue(0);
  const rightOpacity = useTransform(x, [0, 100], [0, 1]);
  const leftOpacity = useTransform(x, [-100, 0], [1, 0]);
  const navigate = useNavigate();
  const [busy, setBusy] = useState(false);
  const [swipingOut, setSwipingOut] = useState<CommitDirection | null>(null);

  const tagClass = cardClass(card);
  const canSwipeLeft = card.kind === 'briefing';

  const onDragEnd = async (_e: unknown, info: PanInfo) => {
    if (busy) return;
    if (info.offset.x > 120) {
      setBusy(true);
      setSwipingOut('right');
      try {
        if (card.kind === 'decision') {
          await api.resolve(card.id);
        } else {
          await api.ack(card.id, 'archived');
        }
        onCommit(card, 'right');
      } catch (e) {
        setBusy(false);
        setSwipingOut(null);
        alert((e as Error).message);
      }
      return;
    }
    if (canSwipeLeft && info.offset.x < -120) {
      setBusy(true);
      setSwipingOut('left');
      try {
        await api.ack(card.id, 'dismissed');
        onCommit(card, 'left');
      } catch (e) {
        setBusy(false);
        setSwipingOut(null);
        alert((e as Error).message);
      }
      return;
    }
    // Snap back via drag's own spring.
  };

  return (
    <motion.article
      className={`card ${tagClass}`}
      drag={busy ? false : 'x'}
      dragConstraints={canSwipeLeft ? { left: -400, right: 400 } : { left: 0, right: 400 }}
      dragElastic={0.4}
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
        navigate(`/cards/${card.id}`);
      }}
    >
      <div className="kind-tag">
        {card.kind === 'decision'
          ? `Decision · ${card.decision?.priority ?? 'medium'}`
          : `Briefing · ${card.briefing?.severity ?? 'info'}`}
      </div>
      <h2>{card.title}</h2>
      <div className="body markdown">
        <ReactMarkdown remarkPlugins={[remarkGfm]}>{card.body}</ReactMarkdown>
      </div>
      <div className="hint">
        {card.kind === 'decision'
          ? `Swipe right to ${recommendedLabel(card) ?? 'approve default'} · tap for options`
          : 'Swipe right 👍 · left 👎 · tap to open'}
      </div>
      <motion.div className="swipe-hint right" style={{ opacity: rightOpacity }}>
        {card.kind === 'decision' ? '✓ Approve' : '👍'}
      </motion.div>
      {canSwipeLeft && (
        <motion.div className="swipe-hint left" style={{ opacity: leftOpacity }}>
          👎
        </motion.div>
      )}
    </motion.article>
  );
}

function cardClass(c: Card): string {
  if (c.kind === 'decision') {
    return `decision priority-${c.decision?.priority ?? 'medium'}`;
  }
  return `briefing severity-${c.briefing?.severity ?? 'info'}`;
}

function recommendedLabel(c: Card): string | null {
  if (c.kind !== 'decision' || !c.decision) return null;
  const rec = c.decision.options.find((o) => o.option_id === c.decision?.recommended_option_id);
  return rec?.label ?? null;
}
