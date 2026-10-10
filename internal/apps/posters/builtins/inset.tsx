import React from "react";
import { CopyBlock, Photo, grounds, type TemplateProps } from "@poster/kit";

export const meta = {
  name: "Inset",
  description: "The photo held inside the margins like a print, zoomed in on a detail",
  photos: { min: 1, max: 1 },
  needs: ["eyebrow", "title"],
};

// A print reads best cropped in on a detail rather than the whole room.
const INSET_ZOOM = 1.6;

export default function Template({ content, format, photos, ground, accent }: TemplateProps) {
  const { width, height, safe } = format;
  const s = Math.min(width, height) / 1080;
  const tall = height >= width;
  const g = grounds[ground];
  const pad = Math.round(56 * s);
  const inner = width - safe.left - safe.right;
  const photo = { ...photos[0], zoom: Math.max(photos[0].zoom ?? 1, INSET_ZOOM) };
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
    return (
      <div
        style={{
          ...canvas,
          flexDirection: "column",
          padding: `${safe.top}px ${safe.right}px ${safe.bottom}px ${safe.left}px`,
        }}
      >
        <Photo photo={photo} style={{ flex: 1, minHeight: Math.round(height * 0.3) }} />
        <CopyBlock
          {...shared}
          width={inner}
          titleMax={Math.round(120 * s)}
          titleLines={2}
          style={{ position: "relative", flexShrink: 0, paddingTop: pad }}
        />
      </div>
    );
  }
  const photoW = Math.round(width * 0.42);
  const w = width - photoW - safe.left - safe.right - Math.round(64 * s);
  return (
    <div style={canvas}>
      <Photo
        photo={photo}
        style={{ position: "absolute", top: safe.top, right: safe.right, bottom: safe.bottom, width: photoW }}
      />
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
