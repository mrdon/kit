import React from "react";
import { CopyBlock, Photo, grounds, type TemplateProps } from "@poster/kit";

export const meta = {
  name: "Full",
  description: "The photo fills the canvas; the copy sits on a solid ground panel flush to the bottom-left",
  photos: { min: 1, max: 1 },
  needs: ["eyebrow", "title"],
};

export default function Template({ content, format, photos, ground, accent }: TemplateProps) {
  const { width, height, safe } = format;
  const s = Math.min(width, height) / 1080;
  const tall = height >= width;
  const g = grounds[ground];
  const pad = Math.round(56 * s);
  // The panel is solid ground, never a scrim or gradient.
  const panelW = tall ? Math.round(width * 0.86) : Math.round(width * 0.46);
  const w = panelW - safe.left - pad;
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
      <Photo photo={photos[0]} style={{ position: "absolute", top: 0, left: 0, width, height }} />
      <CopyBlock
        content={content}
        ground={ground}
        accent={accent}
        s={s}
        width={w}
        titleMax={Math.round(120 * s)}
        titleLines={tall ? 2 : 3}
        style={{
          left: 0,
          bottom: 0,
          top: tall ? undefined : 0,
          width: panelW,
          backgroundColor: g.bg,
          padding: `${tall ? pad : safe.top}px ${pad}px ${safe.bottom}px ${safe.left}px`,
        }}
      />
    </div>
  );
}
