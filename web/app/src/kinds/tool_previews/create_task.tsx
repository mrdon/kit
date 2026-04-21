import type { ToolPreviewProps } from './index';

type CreateTaskArgs = {
  description?: string;
  cron_expr?: string;
  run_at?: string;
  channel_id?: string;
  scope?: string;
};

export function CreateTaskPreview({ args }: ToolPreviewProps) {
  const a = (args ?? {}) as CreateTaskArgs;
  const schedule = formatSchedule(a);

  return (
    <div className="tool-preview tool-preview--create-task">
      <dl className="tool-preview__headers">
        <dt>What</dt>
        <dd>{a.description ?? <em>(no description)</em>}</dd>
        <dt>When</dt>
        <dd>{schedule}</dd>
        {a.channel_id && (
          <>
            <dt>Channel</dt>
            <dd>{a.channel_id}</dd>
          </>
        )}
        {a.scope && a.scope !== 'user' && (
          <>
            <dt>Scope</dt>
            <dd>{a.scope}</dd>
          </>
        )}
      </dl>
    </div>
  );
}

function formatSchedule(a: CreateTaskArgs): React.ReactNode {
  if (a.run_at) {
    return `Once at ${a.run_at}`;
  }
  if (a.cron_expr) {
    return (
      <>
        Recurring: <code>{a.cron_expr}</code>
      </>
    );
  }
  return <em>(not set)</em>;
}
