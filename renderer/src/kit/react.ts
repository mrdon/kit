// The `react` a poster or template imports. Not React: a createElement that
// runs function components eagerly and returns a plain JSON tree the host can
// validate and hand to Satori. No hooks, no context, no state, which is also
// why Satori-targeted layouts cannot use them anyway.

import type { TreeNode as Node } from "../types.ts";
export type { Node };

type Props = Record<string, unknown> & { key?: string; children?: unknown };
type Component = (props: Props) => unknown;

function flatten(children: unknown[], out: Node[] = []): Node[] {
  for (const c of children) {
    if (c === null || c === undefined || typeof c === "boolean") continue;
    if (Array.isArray(c)) {
      flatten(c, out);
      continue;
    }
    if (typeof c === "string" || typeof c === "number") {
      out.push(c);
      continue;
    }
    if (typeof c === "object" && "type" in (c as object)) {
      out.push(c as Node);
      continue;
    }
    throw new Error(`unsupported child: ${typeof c}`);
  }
  return out;
}

export function createElement(type: string | Component, props: Props | null, ...children: unknown[]): Node {
  const all = children.length ? children : props?.children !== undefined ? [props.children] : [];
  if (typeof type === "function") {
    const { children: _ignored, ...rest } = props ?? {};
    const result = type({ ...rest, children: flatten(all) });
    if (Array.isArray(result)) return { type: "__fragment", props: {}, children: flatten(result) };
    return (result ?? null) as Node;
  }
  const { key, children: _c, ...rest } = props ?? {};
  return { type, key: key === undefined ? undefined : String(key), props: rest, children: flatten(all) };
}

export const Fragment: Component = (props) => ({ type: "__fragment", props: {}, children: flatten([props.children]) });

const React = { createElement, Fragment };
export default React;
