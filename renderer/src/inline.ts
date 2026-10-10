// A picked option becomes its own TSX file: the template's code with the
// content inlined. From then on the poster is independent of the template.
import type { Content, PhotoUse } from "./types.ts";

export class InlineError extends Error {}

const DEFAULT_FN = /export\s+default\s+function\s+([A-Za-z_$][\w$]*)\s*\(/;
const DEFAULT_NAME = /export\s+default\s+([A-Za-z_$][\w$]*)\s*;?/;

// Finds the template component's name and strips its default export so the
// poster can add its own.
export function detachTemplate(source: string): { source: string; component: string } {
  let m = source.match(DEFAULT_FN);
  if (m) {
    return { source: source.replace(DEFAULT_FN, `function ${m[1]}(`), component: m[1] };
  }
  m = source.match(DEFAULT_NAME);
  if (m) {
    return { source: source.replace(DEFAULT_NAME, ""), component: m[1] };
  }
  throw new InlineError("the template must `export default function Template(...)`");
}

const MARKER = "// ---- Poster content. The template above draws it; edit either. ----";

export function inlinePoster(templateSource: string, content: Content, photos: PhotoUse[], ground: string, accent: string): string {
  const { source, component } = detachTemplate(templateSource.replace(/export\s+const\s+meta\b/, "const meta"));
  const block = [
    "",
    MARKER,
    `const content = ${JSON.stringify(content, null, 2)};`,
    `const photos = ${JSON.stringify(photos)};`,
    `const ground = ${JSON.stringify(ground)};`,
    `const accent = ${JSON.stringify(accent)};`,
    "",
    "export default function Poster({ format }) {",
    `  return <${component} content={content} format={format} photos={photos} ground={ground} accent={accent} />;`,
    "}",
    "",
  ].join("\n");
  return source.trimEnd() + "\n" + block;
}

// Reads the inlined values back out of a poster, for the facts refresh and
// the save-as-template flow. Returns undefined when the agent restructured
// the file past recognition; callers then fall back to a model.
export function readInlined(posterSource: string): { content?: Content; photos?: PhotoUse[]; ground?: string; accent?: string } {
  const grab = (name: string) => {
    const m = posterSource.match(new RegExp(`\\nconst ${name} = ([\\s\\S]*?);\\n`));
    if (!m) return undefined;
    try {
      return JSON.parse(m[1]);
    } catch {
      return undefined;
    }
  };
  return { content: grab("content"), photos: grab("photos"), ground: grab("ground"), accent: grab("accent") };
}
