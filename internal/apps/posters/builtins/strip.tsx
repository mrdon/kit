import React from "react";
import { CopyFoot, CopyHead, Photo, grounds, type TemplateProps } from "@poster/kit";

export const meta = {
  name: "Strip",
  description: "Like Band, but three photos from the same set side by side",
  photos: { min: 3, max: 3 },
  needs: ["eyebrow", "title"],
};

export default function Template({ content, format, photos, ground, accent }: TemplateProps) {
  const { width, height, safe } = format;
  const s = Math.min(width, height) / 1080;
  const tall = height >= width;
  const g = grounds[ground];
  const pad = Math.round(56 * s);
  const inner = width - safe.left - safe.right;
  const gutter = Math.round(12 * s);
  const shots = photos.slice(0, 3);
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
  const strip = (style: Record<string, number | string>) => (
    <div style={{ display: "flex", gap: gutter, ...style }}>
      {shots.map((p) => (
        <Photo key={p.id} photo={p} style={{ flex: 1, height: "100%" }} />
      ))}
    </div>
  );

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
        {strip({ flex: 1, minHeight: Math.round(height * 0.25) })}
        <CopyFoot {...shared} style={{ padding: `${pad}px ${safe.right}px ${safe.bottom}px ${safe.left}px`, flexShrink: 0 }} />
      </div>
    );
  }
  const headW = Math.round(inner * 0.55);
  return (
    <div style={canvas}>
      {strip({ height: Math.round(height * 0.42), flexShrink: 0 })}
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
