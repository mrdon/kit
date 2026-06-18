import { useEffect, useRef, useState } from 'react';
import { useVoiceRecorder } from './useVoiceRecorder';

type Props = {
  // URL the mic uploads audio to. Passed in so the composer stays
  // surface-agnostic.
  transcribeUrl: string;
  // Where to bounce on a 401 during transcription.
  loginUrl: string;
  // Disabled while an execute request is in flight — prevents double-send.
  busy: boolean;
  onSubmit: (text: string, files: File[]) => void;
  // Override for the textarea placeholder. Falls back to the voice-aware
  // default when omitted.
  placeholder?: string;
  // When set, the composer immediately runs transcription on this blob
  // (skipping the recording phase) and fills the textarea with the
  // result. Used by the quick-chat FAB, which captures audio while the
  // sheet is closed and hands the blob to the composer on open.
  seedAudioBlob?: Blob | null;
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
export default function ChatComposer({
  transcribeUrl,
  loginUrl,
  busy,
  onSubmit,
  placeholder,
  seedAudioBlob,
}: Props) {
  const [text, setText] = useState('');
  const [files, setFiles] = useState<File[]>([]);
  const taRef = useRef<HTMLTextAreaElement | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const recorder = useVoiceRecorder(transcribeUrl, loginUrl);

  // Cap matches the server (internal/attachment.MaxBytes); reject locally
  // so the user gets immediate feedback instead of a failed upload.
  const MAX_FILE_BYTES = 10 * 1024 * 1024;
  const MAX_FILES = 10;
  // Snapshot of the textarea at the moment the user starts holding the
  // mic, so streaming partials append to existing typed content
  // without clobbering it.
  const preRecordRef = useRef<string>('');

  useEffect(() => {
    taRef.current?.focus();
  }, []);

  // When the FAB hands us a pre-captured audio blob, transcribe it on
  // mount. Guarded with a ref so a re-render with the same blob doesn't
  // re-trigger transcription.
  const seedHandledRef = useRef<Blob | null>(null);
  useEffect(() => {
    if (!seedAudioBlob || seedHandledRef.current === seedAudioBlob) return;
    seedHandledRef.current = seedAudioBlob;
    preRecordRef.current = '';
    void recorder.transcribeBlob(seedAudioBlob).then((finalText) => {
      if (finalText) setText(finalText);
      taRef.current?.focus();
    });
  }, [seedAudioBlob, recorder]);

  // Auto-grow the textarea to fit its content up to CSS max-height.
  // Reset to auto first so shrinking works too.
  useEffect(() => {
    const ta = taRef.current;
    if (!ta) return;
    ta.style.height = 'auto';
    ta.style.height = `${ta.scrollHeight}px`;
  }, [text]);

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
    if ((!t && files.length === 0) || busy) return;
    onSubmit(t, files);
    setText('');
    setFiles([]);
  };

  const onPickFiles = (e: React.ChangeEvent<HTMLInputElement>) => {
    const picked = Array.from(e.target.files ?? []);
    e.target.value = ''; // allow re-picking the same file
    setFiles((prev) => {
      const next = [...prev];
      for (const f of picked) {
        if (f.size > MAX_FILE_BYTES) continue; // silently skip oversize
        if (next.length >= MAX_FILES) break;
        next.push(f);
      }
      return next;
    });
  };

  const removeFile = (idx: number) => {
    setFiles((prev) => prev.filter((_, i) => i !== idx));
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

  const canSend = !busy && (text.trim().length > 0 || files.length > 0);

  return (
    <div className="chat-composer">
      {files.length > 0 && (
        <div className="chat-attachments">
          {files.map((f, i) => (
            <span key={`${f.name}-${i}`} className="chat-attachment-chip">
              <span className="chat-attachment-name" title={f.name}>
                {f.type.startsWith('image/') ? '🖼' : '📄'} {f.name}
              </span>
              <button
                type="button"
                className="chat-attachment-remove"
                onClick={() => removeFile(i)}
                aria-label={`Remove ${f.name}`}
              >
                ×
              </button>
            </span>
          ))}
        </div>
      )}
      {(recorder.state === 'recording' || recorder.state === 'transcribing') && (
        <div className="chat-voice-hint" aria-live="polite">
          <span className="chat-voice-dot" />
          <span>
            {recorder.state === 'recording' ? 'Listening…' : 'Transcribing…'}
          </span>
        </div>
      )}
      <textarea
        ref={taRef}
        className="chat-input"
        placeholder={placeholder ?? (recorder.supported ? 'Type or hold mic to talk…' : 'Type a message…')}
        rows={1}
        value={text}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={onKeyDown}
        disabled={busy || recorder.state === 'transcribing'}
      />
      <input
        ref={fileInputRef}
        type="file"
        multiple
        accept="image/*,text/*,application/pdf,application/json,.txt,.md,.csv,.docx,.json,.yaml,.yml,.log,.xml,.tsv"
        style={{ display: 'none' }}
        onChange={onPickFiles}
      />
      <button
        type="button"
        className="chat-attach"
        onClick={() => fileInputRef.current?.click()}
        disabled={busy || files.length >= MAX_FILES}
        aria-label="Attach files"
      >
        📎
      </button>
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
