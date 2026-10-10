import React from "react";
import { CopyFoot, CopyHead, Photo, grounds, type TemplateProps } from "@poster/kit";

export const meta = {
  name: "Band",
  description: "Headline on top, a full-width photo band, details underneath",
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
    flexDirection: "column",
    width,
    height,
    overflow: "hidden",
    backgroundColor: g.bg,
  };
  const shared = { content, ground, accent, s };

  if (tall) {
    return (
      <div style={canvas}>
        <CopyHead
          {...shared}
          width={inner}
          titleMax={Math.round(130 * s)}
          titleLines={2}
          style={{ padding: `${safe.top}px ${safe.right}px ${pad}px ${safe.left}px`, flexShrink: 0 }}
        />
        <Photo photo={photos[0]} style={{ flex: 1, minHeight: Math.round(height * 0.25) }} />
        <CopyFoot {...shared} style={{ padding: `${pad}px ${safe.right}px ${safe.bottom}px ${safe.left}px`, flexShrink: 0 }} />
      </div>
    );
  }
  const headW = Math.round(inner * 0.55);
  return (
    <div style={canvas}>
      <Photo photo={photos[0]} style={{ height: Math.round(height * 0.42), flexShrink: 0 }} />
      <div
        style={{
          display: "flex",
          flex: 1,
          justifyContent: "space-between",
          gap: Math.round(64 * s),
          padding: `${pad}px ${safe.right}px ${safe.bottom}px ${safe.left}px`,
        }}
      >
        <CopyHead {...shared} width={headW} titleMax={Math.round(110 * s)} titleLines={2} style={{ width: headW }} />
        <CopyFoot {...shared} style={{ alignSelf: "flex-end" }} />
      </div>
    </div>
  );
}
