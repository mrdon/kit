import React from "react";
import { CopyBlock, Photo, grounds, type TemplateProps } from "@poster/kit";

export const meta = {
  name: "Split",
  description: "The photo takes one edge of the canvas; the copy sits on the ground beside or below it",
  photos: { min: 1, max: 1 },
  needs: ["eyebrow", "title"],
};

export default function Template({ content, format, photos, ground, accent }: TemplateProps) {
  const { width, height, safe } = format;
  const s = Math.min(width, height) / 1080;
  const tall = height >= width;
  const g = grounds[ground];
  const pad = Math.round(56 * s);
  const inner = width - safe.left - safe.right;
  const canvas = {
    position: "relative",
    boxSizing: "border-box",
    display: "flex",
    width,
    height,
    overflow: "hidden",
    backgroundColor: g.bg,
  };
  const shared = { content, ground, accent, s };

  if (tall) {
    // Copy takes the height it needs; the photo, bleeding off the top and
    // sides, gets whatever is left. Copy can never be pushed off the canvas.
    return (
      <div style={{ ...canvas, flexDirection: "column" }}>
        <Photo photo={photos[0]} style={{ flex: 1, minHeight: Math.round(height * 0.3) }} />
        <CopyBlock
          {...shared}
          width={inner}
          titleMax={Math.round(130 * s)}
          titleLines={2}
          style={{
            position: "relative",
            width,
            flexShrink: 0,
            padding: `${pad}px ${safe.right}px ${safe.bottom}px ${safe.left}px`,
          }}
        />
      </div>
    );
  }
  const photoW = Math.round(width * 0.5);
  const w = width - photoW - safe.left - Math.round(64 * s);
  return (
    <div style={canvas}>
      <Photo photo={photos[0]} style={{ position: "absolute", top: 0, right: 0, width: photoW, height }} />
      <CopyBlock
        {...shared}
        width={w}
        titleMax={Math.round(120 * s)}
        titleLines={3}
        style={{ top: safe.top, left: safe.left, width: w, bottom: safe.bottom }}
      />
    </div>
  );
}
