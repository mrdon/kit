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

  const onCommit = (c: Card) => {
    setCards((cs) => (cs ? cs.filter((x) => x.id !== c.id) : cs));
    if (c.kind === 'decision') {
      setToast('Working on it…');
    } else {
      setToast('Archived');
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
      <div className="feed-title">Kit</div>
      {cards.map((c) => (
        <section key={c.id} className="card-screen">
          <SwipeCard card={c} onCommit={onCommit} />
        </section>
      ))}
      {toast && <div className="toast">{toast}</div>}
    </main>
  );
}

function SwipeCard({ card, onCommit }: { card: Card; onCommit: (c: Card) => void }) {
  const x = useMotionValue(0);
  const rightOpacity = useTransform(x, [0, 100], [0, 1]);
  const navigate = useNavigate();
  const [busy, setBusy] = useState(false);

  const tagClass = cardClass(card);

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
        onCommit(card);
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
      dragConstraints={{ left: 0, right: 300 }}
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
          : 'Swipe right to archive · tap to open'}
      </div>
      <motion.div className="swipe-hint right" style={{ opacity: rightOpacity }}>
        {card.kind === 'decision' ? '✓ Approve' : '✓ Archive'}
      </motion.div>
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

