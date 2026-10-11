import React from "react";
import { CopyBlock, grounds, type TemplateProps } from "@poster/kit";

// Test fixture: the simplest valid template, no photo, so sandbox and
// validator tests have a stable subject that is not a shipped layout.
export const meta = {
  name: "Plain",
  description: "Test fixture: copy on a plain ground",
  photos: { min: 0, max: 0 },
  needs: ["eyebrow", "title"],
};

export default function Template({ content, format, ground, accent }: TemplateProps) {
  const { width, height, safe } = format;
  const s = Math.min(width, height) / 1080;
  const inner = width - safe.left - safe.right;
  return (
    <div style={{ position: "relative", boxSizing: "border-box", display: "flex", width, height, overflow: "hidden", backgroundColor: grounds[ground].bg }}>
      <CopyBlock content={content} ground={ground} accent={accent} s={s} width={inner} titleMax={Math.round(170 * s)} titleLines={3} style={{ top: safe.top, left: safe.left, width: inner, bottom: safe.bottom }} />
    </div>
  );
}
