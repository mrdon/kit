import type { ComponentType } from 'react';
import { SendEmailPreview } from './send_email';
import { PostToChannelPreview } from './post_to_channel';
import { DMUserPreview } from './dm_user';
import { CreateTaskPreview } from './create_task';
import { SchemaPreview } from './schema_preview';

// Preview-component dispatch registry. Per-tool renderers (send_email,
// post_to_channel, …) render the `tool_arguments` JSON in a
// human-friendly layout. They're read-only; the card's Approve / Skip
// buttons and the long-press chat are the interaction surfaces.
//
// Tools that don't register a renderer fall back to SchemaPreview —
// a label/value layout derived from the arguments object. That avoids
// raw JSON for the long tail of tools the agent may gate via the
// universal `require_approval` flag.
export type ToolPreviewProps = {
  args: unknown;
};

export type ToolPreviewComponent = ComponentType<ToolPreviewProps>;

// Per-tool renderers, keyed by tool name.
export const toolPreviews: Record<string, ToolPreviewComponent> = {
  send_email: SendEmailPreview,
  post_to_channel: PostToChannelPreview,
  dm_user: DMUserPreview,
  create_task: CreateTaskPreview,
};

// renderToolPreview picks a per-tool renderer if registered, else
// falls back to SchemaPreview. Returns null when there's nothing to
// show (empty args + unknown tool).
export function renderToolPreview(
  toolName: string | undefined,
  args: unknown,
): React.ReactElement | null {
  if (!toolName && !args) return null;
  const Comp = toolName ? toolPreviews[toolName] : undefined;
  if (Comp) return <Comp args={args} />;
  if (args === undefined || args === null) return null;
  return <SchemaPreview args={args} />;
}
