import type { ComponentType } from 'react';
import { SendEmailPreview } from './send_email';

// Preview-component dispatch registry. Per-tool renderers (send_email,
// create_todo, …) land with their respective tool PRs.
//
// Tool preview components render the `tool_arguments` JSON in a
// human-friendly layout — e.g. send_email renders subject + body +
// recipients. They're read-only; the card's Approve / Skip buttons
// and the long-press chat are the interaction surfaces.
export type ToolPreviewProps = {
  args: unknown;
};

export type ToolPreviewComponent = ComponentType<ToolPreviewProps>;

// Per-tool renderers, keyed by tool name.
export const toolPreviews: Record<string, ToolPreviewComponent> = {
  send_email: SendEmailPreview,
};

// JsonPreview is the fallback when toolPreviews has no entry for
// tool_name. Renders a collapsed <details> block so the user can
// peek at the arguments without dominating the card face.
export function JsonPreview({ args }: ToolPreviewProps) {
  const pretty = (() => {
    try {
      return JSON.stringify(args, null, 2);
    } catch {
      return String(args);
    }
  })();
  return (
    <details className="tool-preview tool-preview--json">
      <summary>View arguments</summary>
      <pre>{pretty}</pre>
    </details>
  );
}

// renderToolPreview picks a per-tool renderer if registered, else
// falls back to JsonPreview. Returns null when there's nothing to
// show (empty args + unknown tool).
export function renderToolPreview(
  toolName: string | undefined,
  args: unknown,
): React.ReactElement | null {
  if (!toolName && !args) return null;
  const Comp = toolName ? toolPreviews[toolName] : undefined;
  if (Comp) return <Comp args={args} />;
  if (args === undefined || args === null) return null;
  return <JsonPreview args={args} />;
}
