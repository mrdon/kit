// Shared LLM chat widget — the hold-to-talk voice + conversational agent
// sheet used by the cards PWA (web/app) and the console (web/console).
// Both vite apps alias `@chat` to this directory; each app supplies its
// own URLs (executeUrl, transcribeUrl, loginUrl) so the widget stays
// origin-agnostic. Import the stylesheet once per app: `@chat/chat.css`.

export { default as ChatSheetBody } from './ChatSheetBody';
export type { ChatSheetBodyProps } from './ChatSheetBody';
export type { ChatTurn } from './useChatStream';
export {
  isAudioCaptureSupported,
  startAudioCapture,
  pickAudioMime,
  type AudioCaptureSession,
} from './audioCapture';
