import { useState } from 'react';
import type { KioskDestination } from './api';

// What a wall screen shows, picked by tapping a button. Kit's own screen
// pages (Menu, Events, Trivia) are the whole everyday vocabulary; typing an
// address is tucked behind "Other address" for the rare outside page.
//
// One tap applies: picking the wrong page is undone by tapping the right one,
// so a separate confirm step would only slow down the person behind the bar.

interface Props {
  destinations: KioskDestination[];
  /** The board's current URL ('' when nothing is assigned). */
  current: string;
  /** Previously shown URLs, newest first; outside pages are offered for reuse. */
  recent?: string[];
  /** Resolve false when the change didn't take, to keep "Other" open. */
  onPick: (url: string) => void | Promise<boolean>;
  disabled?: boolean;
  /** Bigger touch targets, for the bar iPad. */
  large?: boolean;
}

export default function ScreenPicker({ destinations, current, recent, onPick, disabled, large }: Props) {
  const isOwn = destinations.some((d) => d.url === current);
  const custom = current !== '' && !isOwn;
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState(custom ? current : '');
  const [pending, setPending] = useState<string | null>(null);

  const pick = async (url: string): Promise<boolean> => {
    setPending(url);
    try {
      return (await onPick(url)) !== false;
    } finally {
      setPending(null);
    }
  };

  const submitDraft = () => {
    const url = draft.trim();
    if (url === '' || url === current) return;
    void pick(url).then((ok) => ok && setOpen(false));
  };

  const outside = (recent ?? []).filter((u) => u !== current && !destinations.some((d) => d.url === u));
  const btnSize = large ? 'screen-pick-lg' : '';

  return (
    <div className="screen-picker">
      <div className="screen-pick-row">
        {destinations.map((d) => {
          const active = d.url === current;
          return (
            <button
              key={d.key}
              type="button"
              className={`screen-pick ${btnSize} ${active ? 'screen-pick-active' : ''}`}
              aria-pressed={active}
              disabled={disabled || pending !== null || active}
              onClick={() => void pick(d.url)}
            >
              {pending === d.url ? 'Switching…' : d.label}
            </button>
          );
        })}
        <button
          type="button"
          className={`screen-pick screen-pick-other ${btnSize} ${custom ? 'screen-pick-active' : ''}`}
          aria-expanded={open}
          onClick={() => setOpen(!open)}
        >
          {custom ? 'Other address ▾' : 'Other…'}
        </button>
      </div>
      {custom && !open && (
        <p className="screen-pick-note">
          Showing <code title={current}>{current}</code>
        </p>
      )}
      {open && (
        <div className="screen-pick-advanced">
          {/* A div, not a form: the console's "Add a screen" form hosts this. */}
          <div className="device-repoint">
            <input
              type="url"
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault();
                  submitDraft();
                }
              }}
              placeholder="https://"
            />
            <button
              className={`btn ${large ? 'device-btn-sm' : ''}`}
              type="button"
              disabled={disabled || pending !== null || draft.trim() === '' || draft.trim() === current}
              onClick={submitDraft}
            >
              {pending !== null && pending === draft.trim() ? 'Saving…' : 'Show'}
            </button>
          </div>
          {outside.length > 0 && (
            <div className="chip-row">
              {outside.map((u) => (
                <button key={u} type="button" className="chip chip-btn" title={u} onClick={() => setDraft(u)}>
                  {u.replace(/^https?:\/\//, '').slice(0, 40)}
                </button>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
