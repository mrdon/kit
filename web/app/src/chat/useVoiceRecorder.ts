import { useCallback, useEffect, useRef, useState } from 'react';
import { api } from '../api';
import { readSSE } from '../sse';
import { BASENAME } from '../workspace';
import { ChatEvent } from './events';
import { parseEventData } from './parse';
import {
  isAudioCaptureSupported,
  startAudioCapture,
  type AudioCaptureSession,
} from './audioCapture';

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
  // Skip the recording phase and transcribe an externally-captured
  // blob. Used by the quick-chat FAB, which records while the sheet
  // is closed, then hands the blob to the composer on open.
  transcribeBlob: (blob: Blob) => Promise<string>;
};

/**
 * Encapsulates getUserMedia + MediaRecorder + the /chat/transcribe SSE
 * consumption. The composer owns partial text into the textarea: it
 * reads `partial` while recording and replaces the textarea on stop()
 * with the returned final transcript.
 *
 * transcribeUrl is passed in so the hook stays surface-agnostic (card
 * chat and quick chat both hit the same card-less /chat/transcribe
 * today, but this keeps the hook from hardcoding that).
 */
export function useVoiceRecorder(transcribeUrl: string): UseVoiceRecorder {
  const sessionRef = useRef<AudioCaptureSession | null>(null);

  const [supported, setSupported] = useState(false);
  const [state, setState] = useState<'idle' | 'recording' | 'transcribing'>('idle');
  const [partial, setPartial] = useState('');
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setSupported(isAudioCaptureSupported());
  }, []);

  const transcribeBlob = useCallback(
    async (blob: Blob): Promise<string> => {
      if (blob.size === 0) {
        setError('Press and hold the mic longer');
        setState('idle');
        return '';
      }
      setState('transcribing');
      setPartial('');
      setError(null);
      try {
        const resp = await api.chatTranscribe(transcribeUrl, blob);
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
    },
    [transcribeUrl],
  );

  const start = useCallback(async () => {
    if (!supported || state !== 'idle') return;
    setError(null);
    setPartial('');
    try {
      sessionRef.current = await startAudioCapture();
      setState('recording');
    } catch (e) {
      setError((e as Error).message);
      sessionRef.current = null;
    }
  }, [supported, state]);

  const stop = useCallback(async (): Promise<string> => {
    const session = sessionRef.current;
    if (!session || state !== 'recording') return '';
    sessionRef.current = null;
    const blob = await session.stop();
    return transcribeBlob(blob);
  }, [state, transcribeBlob]);

  // Safety net: if the component unmounts mid-recording, release the
  // mic stream. Otherwise the browser's tab indicator stays lit.
  useEffect(() => {
    return () => {
      sessionRef.current?.cancel();
      sessionRef.current = null;
    };
  }, []);

  return { supported, state, partial, error, start, stop, transcribeBlob };
}
