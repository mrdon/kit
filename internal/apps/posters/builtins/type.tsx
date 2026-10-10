import React from "react";
import { CopyBlock, grounds, type TemplateProps } from "@poster/kit";

export const meta = {
  name: "Type",
  description: "No photo: the headline carries the piece on a plain ground",
  photos: { min: 0, max: 0 },
  needs: ["eyebrow", "title"],
};

export default function Template({ content, format, ground, accent }: TemplateProps) {
  const { width, height, safe } = format;
  const s = Math.min(width, height) / 1080;
  const tall = height >= width;
  const g = grounds[ground];
  const inner = width - safe.left - safe.right;
  return (
    <div
      style={{
        position: "relative",
        boxSizing: "border-box",
        display: "flex",
        width,
        height,
        overflow: "hidden",
        backgroundColor: g.bg,
      }}
    >
      <CopyBlock
        content={content}
        ground={ground}
        accent={accent}
        s={s}
        width={tall ? inner : inner * 0.8}
        titleMax={Math.round((tall ? 190 : 170) * s)}
        titleLines={tall ? 4 : 3}
        style={{ top: safe.top, left: safe.left, width: inner, bottom: safe.bottom }}
      />
    </div>
  );
}
