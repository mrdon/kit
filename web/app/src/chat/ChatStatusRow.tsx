import { useEffect, useRef, useState } from 'react';
import { ChatStatus } from './events';

type Props = {
  // "thinking" | "cancelled" | tool name string | "error" | "done"
  status: string;
  // True while a request is in flight; controls whether Stop is shown.
  inFlight: boolean;
  onStop: () => void;
};

/**
 * The always-animated status row shown under the user bubble while a
 * turn is executing. Three liveness cues:
 *   - pulsing "typing" dots (CSS keyframes, not React re-renders)
 *   - status text: "Thinking…" or the current tool name
 *   - elapsed counter, ticking once per second after 2s, via rAF so it
 *     doesn't force transcript re-renders every tick
 *   - Stop button to abort the fetch
 */
export default function ChatStatusRow({ status, inFlight, onStop }: Props) {
  const elapsed = useRafSeconds(inFlight);
  if (!inFlight && status !== 'error') return null;

  let label: string;
  if (status === ChatStatus.Thinking || status === '') label = 'Thinking…';
  else if (status === ChatStatus.Cancelled) label = 'Stopped.';
  else label = describeTool(status);

  const showCounter = inFlight && elapsed >= 2;

  return (
    <div className="chat-status-row">
      {inFlight && (
        <span className="chat-typing" aria-hidden>
          <span />
          <span />
          <span />
        </span>
      )}
      <span className="chat-status-label">{label}</span>
      {showCounter && <span className="chat-status-elapsed">· {elapsed}s</span>}
      {inFlight && (
        <button type="button" className="chat-stop" onClick={onStop}>
          Stop
        </button>
      )}
    </div>
  );
}

function describeTool(name: string): string {
  // The plan defers per-tool friendly labels. For now, format the raw
  // tool name: "complete_todo" -> "Running complete_todo…".
  return `Running ${name}…`;
}

// useRafSeconds returns integer seconds elapsed since `active` flipped
// to true. Uses requestAnimationFrame so a running counter doesn't tie
// itself to React's render loop — the returned value only changes when
// the floor(seconds) ticks.
function useRafSeconds(active: boolean): number {
  const [sec, setSec] = useState(0);
  const startRef = useRef<number | null>(null);

  useEffect(() => {
    if (!active) {
      startRef.current = null;
      setSec(0);
      return;
    }
    let rafID = 0;
    startRef.current = performance.now();
    let lastInt = 0;
    const tick = () => {
      const s = Math.floor((performance.now() - (startRef.current ?? 0)) / 1000);
      if (s !== lastInt) {
        lastInt = s;
        setSec(s);
      }
      rafID = requestAnimationFrame(tick);
    };
    rafID = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(rafID);
  }, [active]);

  return sec;
}
