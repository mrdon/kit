import { useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { StackViews, type StackView } from '../types';
import { VIEW_LABELS, VIEW_PATHS } from './views';

const VIEW_ORDER: StackView[] = [StackViews.feed, StackViews.tasks];

// ViewMenu is the ☰ switcher in the card's empty top-right corner. Outside
// the feed it also names the view, so a stack of task cards is never
// mistaken for "everything that needs you".
export default function ViewMenu({ current }: { current: StackView }) {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement | null>(null);
  const navigate = useNavigate();

  useEffect(() => {
    if (!open) return;
    const onPointerDown = (e: PointerEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    document.addEventListener('pointerdown', onPointerDown);
    window.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('pointerdown', onPointerDown);
      window.removeEventListener('keydown', onKey);
    };
  }, [open]);

  const choose = (view: StackView) => {
    setOpen(false);
    // Replace, not push: flipping views is a mode change, and a history
    // entry per flip would turn the Android back button into an undo
    // stack of toggles.
    if (view !== current) navigate(VIEW_PATHS[view], { replace: true });
  };

  return (
    <div className="view-menu" ref={rootRef}>
      <button
        type="button"
        className="view-menu-button"
        aria-label="Switch view"
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
      >
        <span aria-hidden>☰</span>
        {current !== StackViews.feed && <span>{VIEW_LABELS[current]}</span>}
      </button>
      {open && (
        <div className="view-menu-list" role="menu">
          {VIEW_ORDER.map((v) => (
            <button
              key={v || 'feed'}
              type="button"
              role="menuitemradio"
              aria-checked={v === current}
              className={`view-menu-item${v === current ? ' is-current' : ''}`}
              onClick={() => choose(v)}
            >
              <span>{VIEW_LABELS[v]}</span>
              {v === current && <span aria-hidden>✓</span>}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
