// Small helper shared between useChatStream and useVoiceRecorder. Event
// data arrives as a string we control on the server; try to JSON-parse
// it, returning {} on anything weird so the caller's switch statements
// stay simple.

export function parseEventData(s: string): Record<string, unknown> {
  try {
    return JSON.parse(s) as Record<string, unknown>;
  } catch {
    return {};
  }
}
