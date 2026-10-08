import { useEffect, useRef, useState } from 'react';
import {
  isAudioCaptureSupported,
  startAudioCapture,
  type AudioCaptureSession,
} from '@chat';

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

export default function QuickChatFab({
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
    // Queue a delayed buzz so it fires the moment the arm timer
    // expires. vibrate() needs to be called from inside a real user
    // gesture (pointerdown counts; setTimeout callbacks don't) — so we
    // schedule the haptic here using a [silent-wait, vibrate] pattern
    // and cancel it below if the user releases before the threshold.
    try {
      navigator.vibrate?.([0, LONG_PRESS_MS, 80]);
    } catch {
      // ignore — vibrate is best-effort, unsupported on iOS
    }
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
      // Cancel the queued long-press buzz so it doesn't fire after a
      // short tap.
      try {
        navigator.vibrate?.(0);
      } catch {
        // ignore
      }
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
    if (!recording) {
      cancelArming();
      try {
        navigator.vibrate?.(0);
      } catch {
        // ignore
      }
    }
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
