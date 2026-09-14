import { useEffect, useRef, useState } from 'react';

// A number box a host can actually type into.
//
// A controlled <input type="number"> bound straight to a number cannot be
// emptied: backspacing to nothing yields Number('') === 0, the 0 is written
// back, and the caret sits behind a digit nobody typed. The only way to
// change "5" to "12" was the spinner arrows, which are eight pixels wide.
//
// So the box holds a STRING draft and commits only when the draft parses to
// a whole number inside the bounds. Empty, "-", or out of range are allowed
// on screen while the host is mid-keystroke; on blur an unfinished draft
// snaps back to the last committed value rather than saving a zero. Plain
// text with a numeric keyboard hint, so there are no arrows to miss.
export function NumberField({ value, min, max, disabled, onCommit }: {
  value: number;
  min: number;
  max?: number;
  disabled?: boolean;
  onCommit: (n: number) => void;
}) {
  const [draft, setDraft] = useState(String(value));
  const focused = useRef(false);

  // Something else changed the value (rows added, a saved game reloaded):
  // follow it, but never overwrite what the host is typing right now.
  useEffect(() => {
    if (!focused.current) setDraft(String(value));
  }, [value]);

  const parse = (raw: string): number | null => {
    if (!/^\d+$/.test(raw.trim())) return null;
    const n = Number(raw);
    if (n < min || (max !== undefined && n > max)) return null;
    return n;
  };

  return (
    <input
      type="text"
      inputMode="numeric"
      pattern="[0-9]*"
      value={draft}
      disabled={disabled}
      onFocus={() => { focused.current = true; }}
      onChange={(e) => {
        setDraft(e.target.value);
        const n = parse(e.target.value);
        if (n !== null && n !== value) onCommit(n);
      }}
      onBlur={() => {
        focused.current = false;
        if (parse(draft) === null) setDraft(String(value));
      }}
    />
  );
}
