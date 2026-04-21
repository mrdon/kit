import type { ToolPreviewProps } from './index';

// SchemaPreview is the fallback rendering for gated tool calls that
// don't have a custom preview component registered. Renders the
// tool_arguments as a label/value list so users see the parameters in
// a readable layout instead of raw JSON. Keys get titlecased, long
// strings get a preformatted block, arrays bullet-list, and booleans
// show as pill chips.
export function SchemaPreview({ args }: ToolPreviewProps) {
  if (args === null || args === undefined) return null;
  if (typeof args !== 'object' || Array.isArray(args)) {
    return (
      <div className="tool-preview tool-preview--schema">
        <div className="tool-preview__body">
          <ValueCell value={args} />
        </div>
      </div>
    );
  }
  const entries = Object.entries(args as Record<string, unknown>).filter(
    ([, v]) => v !== undefined && v !== null && v !== '',
  );
  if (entries.length === 0) return null;
  return (
    <div className="tool-preview tool-preview--schema">
      <dl className="tool-preview__headers">
        {entries.map(([key, value]) => (
          <Row key={key} label={key} value={value} />
        ))}
      </dl>
    </div>
  );
}

function Row({ label, value }: { label: string; value: unknown }) {
  return (
    <>
      <dt>{humanise(label)}</dt>
      <dd>
        <ValueCell value={value} />
      </dd>
    </>
  );
}

function ValueCell({ value }: { value: unknown }) {
  if (value === null || value === undefined) {
    return <em className="tool-preview__muted">(empty)</em>;
  }
  if (typeof value === 'boolean') {
    return (
      <span
        className={
          value
            ? 'tool-preview__pill tool-preview__pill--yes'
            : 'tool-preview__pill tool-preview__pill--no'
        }
      >
        {value ? 'yes' : 'no'}
      </span>
    );
  }
  if (typeof value === 'number') {
    return <span>{value}</span>;
  }
  if (Array.isArray(value)) {
    if (value.length === 0) {
      return <em className="tool-preview__muted">(none)</em>;
    }
    return (
      <ul className="tool-preview__list">
        {value.map((item, idx) => (
          <li key={idx}>
            <ValueCell value={item} />
          </li>
        ))}
      </ul>
    );
  }
  if (typeof value === 'object') {
    return (
      <pre className="tool-preview__json-inline">
        {JSON.stringify(value, null, 2)}
      </pre>
    );
  }
  const s = String(value);
  if (s.includes('\n') || s.length > 80) {
    return <pre className="tool-preview__multiline">{s}</pre>;
  }
  return <span>{s}</span>;
}

function humanise(key: string): string {
  return key
    .replace(/_/g, ' ')
    .replace(/\b\w/g, (c) => c.toUpperCase());
}
