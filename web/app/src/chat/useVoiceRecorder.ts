import { useCallback, useEffect, useRef, useState } from 'react';
import { api } from '../api';
import { readSSE } from '../sse';
import { BASENAME } from '../workspace';
import { ChatEvent } from './events';
import { parseEventData } from './parse';

// Supported MIME types in preference order. Chrome/Firefox desktop and
// most Android browsers support webm/opus; iOS Safari falls back to
// mp4 (AAC). The first one MediaRecorder.isTypeSupported accepts wins.
const MIME_CANDIDATES = [
  'audio/webm;codecs=opus',
  'audio/webm',
  'audio/mp4',
] as const;

function pickMime(): string | null {
  if (typeof MediaRecorder === 'undefined') return null;
  for (const m of MIME_CANDIDATES) {
    if (MediaRecorder.isTypeSupported(m)) return m;
  }
  return null;
}

export type UseVoiceRecorder = {
  // true only when MediaRecorder + a supported MIME + mic permission
  // API are all present. Render the mic button only when this is true.
  supported: boolean;
  // Current recording state. "idle" | "recording" | "transcribing".
  state: 'idle' | 'recording' | 'transcribing';
  // Partial transcript as it streams in. Replaced by final on release.
  partial: string;
  // Error message from any failure, cleared on next start().
  error: string | null;
  // Start recording. No-op if unsupported or already recording.
  start: () => Promise<void>;
  // Stop recording and kick off transcription. Returns the final
  // transcript, or '' on error/abort. start() -> stop() is the
  // hold-and-release pattern.
  stop: () => Promise<string>;
};

type CardRef = { sourceApp: string; kind: string; id: string };

/**
 * Encapsulates getUserMedia + MediaRecorder + the /chat/transcribe SSE
 * consumption. The composer owns partial text into the textarea: it
 * reads `partial` while recording and replaces the textarea on stop()
 * with the returned final transcript.
 */
export function useVoiceRecorder(card: CardRef): UseVoiceRecorder {
  const mimeRef = useRef<string | null>(null);
  const recorderRef = useRef<MediaRecorder | null>(null);
  const chunksRef = useRef<Blob[]>([]);
  const streamRef = useRef<MediaStream | null>(null);

  const [supported, setSupported] = useState(false);
  const [state, setState] = useState<'idle' | 'recording' | 'transcribing'>('idle');
  const [partial, setPartial] = useState('');
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const hasGUM = !!navigator.mediaDevices?.getUserMedia;
    const m = pickMime();
    setSupported(hasGUM && m !== null);
    mimeRef.current = m;
  }, []);

  const cleanup = useCallback(() => {
    recorderRef.current = null;
    chunksRef.current = [];
    const s = streamRef.current;
    if (s) {
      for (const track of s.getTracks()) track.stop();
      streamRef.current = null;
    }
  }, []);

  const start = useCallback(async () => {
    if (!supported || state !== 'idle') return;
    setError(null);
    setPartial('');
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      streamRef.current = stream;
      const mime = mimeRef.current ?? undefined;
      const rec = new MediaRecorder(stream, mime ? { mimeType: mime } : undefined);
      recorderRef.current = rec;
      chunksRef.current = [];
      rec.ondataavailable = (e) => {
        if (e.data && e.data.size > 0) chunksRef.current.push(e.data);
      };
      rec.start();
      setState('recording');
    } catch (e) {
      setError((e as Error).message);
      cleanup();
    }
  }, [supported, state, cleanup]);

  const stop = useCallback(async (): Promise<string> => {
    const rec = recorderRef.current;
    if (!rec || state !== 'recording') return '';
    setState('transcribing');

    // Wait for the recorder to flush its last chunk, then collect the
    // buffered audio into a single Blob.
    const blob = await new Promise<Blob>((resolve) => {
      rec.onstop = () => {
        const type = rec.mimeType || mimeRef.current || 'audio/webm';
        resolve(new Blob(chunksRef.current, { type }));
      };
      rec.stop();
    });
    cleanup();

    if (blob.size === 0) {
      // Very short presses finish before MediaRecorder ever fires
      // dataavailable, so there's nothing to transcribe. Bail out
      // with a hint rather than submitting an empty multipart.
      setError('Press and hold the mic longer');
      setState('idle');
      return '';
    }

    try {
      const resp = await api.chatTranscribe(card.sourceApp, card.kind, card.id, blob);
      if (resp.status === 401) {
        window.location.href = BASENAME + '/login';
        return '';
      }
      if (!resp.ok) {
        // The server uses http.Error() for pre-stream rejections, so
        // the body is a short plain-text reason. Fall back to the
        // status text if the body is unreadable.
        const reason = (await resp.text().catch(() => '')) || `${resp.status} ${resp.statusText}`;
        setError(reason.trim());
        setState('idle');
        return '';
      }
      let finalText = '';
      for await (const frame of readSSE(resp)) {
        switch (frame.event) {
          case ChatEvent.Partial: {
            const d = parseEventData(frame.data) as { text?: string };
            if (typeof d.text === 'string') {
              // whisper emits segments; accumulate them separated by a
              // space so the textarea reads naturally while streaming.
              setPartial((p) => (p ? p + ' ' + d.text : (d.text ?? '')));
            }
            break;
          }
          case ChatEvent.Final: {
            const d = parseEventData(frame.data) as { text?: string };
            if (typeof d.text === 'string') finalText = d.text;
            break;
          }
          case ChatEvent.Error: {
            const d = parseEventData(frame.data) as { message?: string };
            setError(d.message ?? 'transcription failed');
            break;
          }
        }
      }
      setState('idle');
      return finalText;
    } catch (e) {
      setError((e as Error).message);
      setState('idle');
      return '';
    }
  }, [state, cleanup, card.sourceApp, card.kind, card.id]);

  // Safety net: if the component unmounts mid-recording, release the
  // mic stream. Otherwise the browser's tab indicator stays lit.
  useEffect(() => cleanup, [cleanup]);

  return { supported, state, partial, error, start, stop };
}

