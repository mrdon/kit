import { useEffect, useRef, useState } from 'react';
import { useVoiceRecorder } from './useVoiceRecorder';

type CardRef = { sourceApp: string; kind: string; id: string };

type Props = {
  card: CardRef;
  // Disabled while an execute request is in flight — prevents double-send.
  busy: boolean;
  onSubmit: (text: string) => void;
};

/**
 * Composer row: textarea + mic button + send button.
 *
 * Typed path: Enter sends, Shift+Enter adds a newline.
 *
 * Voice path: pointerdown on the mic starts recording; live whisper
 * partials stream into the textarea value; pointerup triggers
 * transcription and replaces the field with the final transcript. The
 * textarea is still editable after — the user always reviews before
 * sending.
 *
 * If MediaRecorder/getUserMedia aren't available the mic is hidden and
 * the UI remains fully usable for typing.
 */
export default function ChatComposer({ card, busy, onSubmit }: Props) {
  const [text, setText] = useState('');
  const taRef = useRef<HTMLTextAreaElement | null>(null);
  const recorder = useVoiceRecorder(card);
  // Snapshot of the textarea at the moment the user starts holding the
  // mic, so streaming partials append to existing typed content
  // without clobbering it.
  const preRecordRef = useRef<string>('');

  useEffect(() => {
    taRef.current?.focus();
  }, []);

  // Pipe voice partials into the textarea while recording OR while
  // waiting for the final (transcribing) event. Stopping too early
  // hides partials that arrive after release but before final lands,
  // so the user sees nothing during the "Transcribing…" phase.
  useEffect(() => {
    if (recorder.state === 'idle') return;
    const stitched = joinPreAndPartial(preRecordRef.current, recorder.partial);
    setText(stitched);
  }, [recorder.partial, recorder.state]);

  const submit = () => {
    const t = text.trim();
    if (!t || busy) return;
    onSubmit(t);
    setText('');
  };

  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    // nativeEvent.isComposing is true while an IME is mid-composition
    // (e.g. accents, CJK). Enter in that state commits the IME rather
    // than sending the message, so let it pass through.
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      submit();
    }
  };

  const onMicDown = async (e: React.PointerEvent) => {
    if (!recorder.supported || recorder.state !== 'idle' || busy) return;
    e.preventDefault();
    preRecordRef.current = text;
    await recorder.start();
  };

  const onMicUp = async () => {
    if (recorder.state !== 'recording') return;
    const finalText = await recorder.stop();
    // Prefer whisper's final result (it's more accurate than partials).
    if (finalText) {
      setText(joinPreAndPartial(preRecordRef.current, finalText));
    }
    preRecordRef.current = '';
    taRef.current?.focus();
  };

  const canSend = !busy && text.trim().length > 0;

  return (
    <div className="chat-composer">
      <textarea
        ref={taRef}
        className="chat-input"
        placeholder={recorder.supported ? 'Type or hold mic to talk…' : 'Type a message…'}
        rows={1}
        value={text}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={onKeyDown}
        disabled={busy || recorder.state === 'transcribing'}
      />
      {recorder.supported && (
        <button
          type="button"
          className={`chat-mic${recorder.state === 'recording' ? ' recording' : ''}`}
          onPointerDown={onMicDown}
          onPointerUp={onMicUp}
          onPointerCancel={onMicUp}
          onPointerLeave={(e) => {
            if (recorder.state === 'recording' && e.currentTarget.hasPointerCapture(e.pointerId)) {
              void onMicUp();
            }
          }}
          disabled={busy || recorder.state === 'transcribing'}
          aria-label={recorder.state === 'recording' ? 'Release to transcribe' : 'Hold to talk'}
        >
          {recorder.state === 'transcribing' ? '…' : '🎙'}
        </button>
      )}
      <button
        type="button"
        className="chat-send"
        onClick={submit}
        disabled={!canSend}
        aria-label="Send"
      >
        ▶
      </button>
      {recorder.error && <div className="chat-error">{recorder.error}</div>}
    </div>
  );
}

function joinPreAndPartial(pre: string, partial: string): string {
  if (!partial) return pre;
  if (!pre) return partial;
  return pre.endsWith(' ') || pre.endsWith('\n') ? pre + partial : pre + ' ' + partial;
}
