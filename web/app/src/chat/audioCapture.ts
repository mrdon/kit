// Shared MediaRecorder primitives for both the chat composer's mic
// (press-and-hold) and the quick-chat FAB (long-press to arm, tap to
// stop). Keeps mime selection and stream cleanup in one place so the
// two surfaces don't drift on browser-support edge cases.

const MIME_CANDIDATES = [
  'audio/webm;codecs=opus',
  'audio/webm',
  'audio/mp4',
] as const;

export function pickAudioMime(): string | null {
  if (typeof MediaRecorder === 'undefined') return null;
  for (const m of MIME_CANDIDATES) {
    if (MediaRecorder.isTypeSupported(m)) return m;
  }
  return null;
}

export function isAudioCaptureSupported(): boolean {
  if (!navigator.mediaDevices?.getUserMedia) return false;
  return pickAudioMime() !== null;
}

export type AudioCaptureSession = {
  // Stop recording and return the captured Blob. Resolves to a
  // zero-byte blob if MediaRecorder never fired dataavailable (very
  // short presses) — the caller should treat that as "too brief".
  stop: () => Promise<Blob>;
  // Abort recording without producing a blob (e.g. component unmount,
  // user cancel). Releases the mic stream.
  cancel: () => void;
};

// startAudioCapture acquires the mic and starts recording. Throws if
// permission is denied or the device is unavailable. Caller must call
// stop() or cancel() exactly once.
export async function startAudioCapture(): Promise<AudioCaptureSession> {
  const mime = pickAudioMime();
  const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
  const rec = new MediaRecorder(stream, mime ? { mimeType: mime } : undefined);
  const chunks: Blob[] = [];
  rec.ondataavailable = (e) => {
    if (e.data && e.data.size > 0) chunks.push(e.data);
  };
  rec.start();

  const releaseStream = () => {
    for (const track of stream.getTracks()) track.stop();
  };

  let settled = false;
  return {
    stop: () =>
      new Promise<Blob>((resolve) => {
        if (settled) {
          resolve(new Blob([], { type: mime ?? 'audio/webm' }));
          return;
        }
        settled = true;
        rec.onstop = () => {
          const type = rec.mimeType || mime || 'audio/webm';
          releaseStream();
          resolve(new Blob(chunks, { type }));
        };
        try {
          rec.stop();
        } catch {
          releaseStream();
          resolve(new Blob(chunks, { type: mime ?? 'audio/webm' }));
        }
      }),
    cancel: () => {
      if (settled) return;
      settled = true;
      try {
        rec.stop();
      } catch {
        // ignore — the only reason stop() throws is "not recording", and
        // we're cleaning up anyway.
      }
      releaseStream();
    },
  };
}
