// Event names emitted by /api/v1/stack/items/.../chat/transcribe and
// .../chat/execute. Mirror of internal/chat/events.go. No raw string
// literals at call sites — import a ChatEvent constant instead.

export const ChatEvent = {
  Partial: 'partial',
  Final: 'final',
  Status: 'status',
  Tool: 'tool',
  Response: 'response',
  Done: 'done',
  Error: 'error',
} as const;
export type ChatEventType = typeof ChatEvent[keyof typeof ChatEvent];

export const ChatStatus = {
  Thinking: 'thinking',
  Cancelled: 'cancelled',
} as const;
export type ChatStatusType = typeof ChatStatus[keyof typeof ChatStatus];

// Discriminated-union payloads for parsed events. The frontend
// switches on `event.event` so each branch gets narrowed data.
export type PartialPayload = { text: string };
export type FinalPayload = { text: string };
export type StatusPayload = { status: ChatStatusType };
export type ToolPayload = { name: string };
export type ResponsePayload = { text: string };
export type DonePayload = { removed_ids?: string[] };
export type ErrorPayload = { message: string };

export type ChatEventFrame =
  | { event: typeof ChatEvent.Partial; data: PartialPayload }
  | { event: typeof ChatEvent.Final; data: FinalPayload }
  | { event: typeof ChatEvent.Status; data: StatusPayload }
  | { event: typeof ChatEvent.Tool; data: ToolPayload }
  | { event: typeof ChatEvent.Response; data: ResponsePayload }
  | { event: typeof ChatEvent.Done; data: DonePayload }
  | { event: typeof ChatEvent.Error; data: ErrorPayload };
