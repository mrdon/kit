import { useCallback, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { motion, useMotionValue, useTransform, type PanInfo } from 'framer-motion';
import { api } from './api';
import type { Card } from './types';

export default function Stack() {
  const [cards, setCards] = useState<Card[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [toast, setToast] = useState<string | null>(null);

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

  const onCommit = (c: Card, direction: 'right' | 'left') => {
    setCards((cs) => (cs ? cs.filter((x) => x.id !== c.id) : cs));
    if (c.kind === 'decision') {
      setToast(direction === 'right' ? 'Working on it…' : 'Skipped');
    } else {
      setToast(direction === 'right' ? '👍 Archived' : '👎 Dismissed');
    }
    setTimeout(() => setToast(null), 2000);
  };

  if (err) return <div className="empty">Error: {err}</div>;
  if (cards === null) return <div className="empty">Loading…</div>;
  if (cards.length === 0) {
    return <div className="empty"><div>Nothing needs you right now.</div></div>;
  }

  return (
    <main className="feed">
      {cards.map((c) => (
        <section key={c.id} className="card-screen">
          <SwipeCard card={c} onCommit={onCommit} />
        </section>
      ))}
      {toast && <div className="toast">{toast}</div>}
    </main>
  );
}

function SwipeCard({ card, onCommit }: { card: Card; onCommit: (c: Card, direction: 'right' | 'left') => void }) {
  const x = useMotionValue(0);
  const rightOpacity = useTransform(x, [0, 100], [0, 1]);
  const leftOpacity = useTransform(x, [-100, 0], [1, 0]);
  const navigate = useNavigate();
  const [busy, setBusy] = useState(false);

  const tagClass = cardClass(card);
  const canSwipeLeft = card.kind === 'briefing'; // decisions need an explicit option; no default "no"

  const onDragEnd = async (_e: unknown, info: PanInfo) => {
    if (busy) return;
    if (info.offset.x > 120) {
      setBusy(true);
      try {
        if (card.kind === 'decision') {
          await api.resolve(card.id);
        } else {
          await api.ack(card.id, 'archived');
        }
        onCommit(card, 'right');
      } catch (e) {
        setBusy(false);
        alert((e as Error).message);
      }
      return;
    }
    if (canSwipeLeft && info.offset.x < -120) {
      setBusy(true);
      try {
        await api.ack(card.id, 'dismissed');
        onCommit(card, 'left');
      } catch (e) {
        setBusy(false);
        alert((e as Error).message);
      }
      return;
    }
    // Snap back.
  };

  return (
    <motion.article
      className={`card ${tagClass}`}
      drag="x"
      dragConstraints={canSwipeLeft ? { left: -300, right: 300 } : { left: 0, right: 300 }}
      dragElastic={0.4}
      style={{ x }}
      onDragEnd={onDragEnd}
      onClick={() => navigate(`/cards/${card.id}`)}
    >
      <div className="kind-tag">
        {card.kind === 'decision'
          ? `Decision · ${card.decision?.priority ?? 'medium'}`
          : `Briefing · ${card.briefing?.severity ?? 'info'}`}
      </div>
      <h2>{card.title}</h2>
      <div className="body">{card.body}</div>
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
