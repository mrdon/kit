import { useEffect, useRef, useState } from 'react';

export type ActionMenuItem = {
  label: string;
  onClick: () => void;
  // danger styles the item in the console's error ink — for destructive
  // actions (e.g. "Destroy vault") that shouldn't sit in the open as a
  // full button.
  danger?: boolean;
};

// ActionMenu is a cog-triggered dropdown for a page's secondary and
// destructive actions, keeping them out of the primary button row. It
// wears the console's panel/line/radius tokens so it reads as native
// chrome, and closes on outside-click or Escape. Anchor it inside a
// .page-head-actions row next to the page's primary button.
export default function ActionMenu({
  label = 'Settings',
  items,
}: {
  label?: string;
  items: ActionMenuItem[];
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  return (
    <div className="action-menu" ref={ref}>
      <button
        type="button"
        className="icon-btn"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={label}
        onClick={() => setOpen((v) => !v)}
      >
        ⚙
      </button>
      {open && (
        <div className="action-menu-pop" role="menu">
          {items.map((it) => (
            <button
              key={it.label}
              type="button"
              role="menuitem"
              className={`action-menu-item${it.danger ? ' action-menu-item-danger' : ''}`}
              onClick={() => {
                setOpen(false);
                it.onClick();
              }}
            >
              {it.label}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
