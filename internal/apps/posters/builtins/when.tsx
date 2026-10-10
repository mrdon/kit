import React from "react";
import { CopyFoot, CopyHead, Text, accents, fitTitle, font, grounds, weights, type TemplateProps } from "@poster/kit";

export const meta = {
  name: "When",
  description: "No photo: the day and time are the display type, the headline comes second, a short accent bar is the one accent element",
  photos: { min: 0, max: 0 },
  needs: ["eyebrow", "title"],
};

export default function Template({ content, format, ground, accent }: TemplateProps) {
  const { width, height, safe } = format;
  const s = Math.min(width, height) / 1080;
  const tall = height >= width;
  const g = grounds[ground];
  const a = accents[accent];
  const pad = Math.round(56 * s);
  const inner = width - safe.left - safe.right;
  const w = tall ? inner : Math.round(inner * 0.7);
  const time = content.details.find((d) => d.label === "When")?.value;
  // One line if it can be set big; two only when one line would go small.
  // A wide canvas has less height to spend, so the display lines shrink.
  const big = Math.round((tall ? 220 : 150) * s);
  const one = fitTitle(content.eyebrow, w, big, 1);
  const size = one >= big / 2 ? one : fitTitle(content.eyebrow, w, Math.round(big * 0.73), 2);
  const shared = { content, ground, accent, s };
  return (
    <div
      style={{
        position: "relative",
        boxSizing: "border-box",
        display: "flex",
        flexDirection: "column",
        width,
        height,
        overflow: "hidden",
        backgroundColor: g.bg,
        padding: `${safe.top}px ${safe.right}px ${safe.bottom}px ${safe.left}px`,
      }}
    >
      <div style={{ display: "flex", flexDirection: "column", color: g.text, fontFamily: font.display, fontWeight: weights.display }}>
        <div style={{ width: Math.round(120 * s), height: Math.round(16 * s), backgroundColor: a.fill }} />
        <div style={{ display: "flex", marginTop: Math.round(36 * s), fontSize: size, lineHeight: 0.92, letterSpacing: "-0.01em" }}>
          <Text>{content.eyebrow}</Text>
        </div>
        {time ? (
          <div style={{ display: "flex", marginTop: Math.round(12 * s), fontSize: Math.round(size * 0.55), lineHeight: 1 }}>
            <Text>{time}</Text>
          </div>
        ) : null}
      </div>
      <CopyHead
        {...shared}
        eyebrow={false}
        width={w}
        titleMax={Math.round((tall ? 110 : 90) * s)}
        titleLines={tall ? 3 : 2}
        style={{ marginTop: Math.round((tall ? 64 : 40) * s), width: w }}
      />
      <CopyFoot {...shared} skipDetail="When" style={{ marginTop: "auto", paddingTop: pad }} />
    </div>
  );
}
