import React from "react";
import { Eyebrow, Logo, Photo, Text, fitTitle, font, grounds, logoWidth, weights, type TemplateProps } from "@poster/kit";

export const meta = {
  name: "Corners",
  description: "Eyebrow and logo in the top corners, a huge title low on the left, the facts right-aligned low on the right, air or a photo between",
  photos: { min: 0, max: 1 },
  needs: ["eyebrow", "title"],
};

export default function Template({ content, format, photos, ground, accent }: TemplateProps) {
  const { width, height, safe } = format;
  const s = Math.min(width, height) / 1080;
  const tall = height >= width;
  const g = grounds[ground];
  const pad = Math.round(56 * s);
  const inner = width - safe.left - safe.right;
  const photo = photos[0];
  const titleW = Math.round((tall ? inner : width * 0.55 - safe.left - pad) * 0.66);
  const titleSize = fitTitle(content.title, titleW, Math.round((photo ? 130 : 170) * s), photo ? 2 : 3);
  const top = (
    <div style={{ display: "flex", justifyContent: "space-between", alignItems: "flex-start", padding: `${safe.top}px ${safe.right}px 0 ${safe.left}px`, flexShrink: 0 }}>
      <Eyebrow content={content} accent={accent} s={s} />
      <Logo variant={g.logo} width={logoWidth(s)} />
    </div>
  );
  const bottom = (
    <div style={{ display: "flex", alignItems: "flex-end", justifyContent: "space-between", gap: Math.round(48 * s), padding: `0 ${safe.right}px ${safe.bottom}px ${safe.left}px`, flexShrink: 0 }}>
      <h1 style={{ margin: 0, width: titleW, fontFamily: font.display, fontWeight: weights.display, fontSize: titleSize, lineHeight: 0.92, letterSpacing: "-0.015em", whiteSpace: "pre-line", textWrap: "balance" }}>
        <Text>{content.title}</Text>
      </h1>
      <div style={{ display: "flex", flexDirection: "column", alignItems: "flex-end", textAlign: "right", gap: Math.round(14 * s), maxWidth: Math.round(inner * 0.32) }}>
        {content.details.map((d) => (
          <div key={d.label} style={{ display: "flex", flexDirection: "column", alignItems: "flex-end" }}>
            <span style={{ fontSize: Math.round(22 * s), fontWeight: weights.textBold, color: g.label }}>{d.label}</span>
            <span style={{ fontSize: Math.round(30 * s), lineHeight: 1.25 }}>
              <Text>{d.value}</Text>
            </span>
          </div>
        ))}
        {content.action ? (
          <span style={{ fontSize: Math.round(28 * s), fontWeight: weights.textBold, lineHeight: 1.3 }}>
            <Text>{content.action}</Text>
          </span>
        ) : null}
      </div>
    </div>
  );
  const canvas = { boxSizing: "border-box", display: "flex", width, height, overflow: "hidden", backgroundColor: g.bg, color: g.text, fontFamily: font.text } as const;
  const middle = photo ? (
    <Photo photo={photo} style={{ flex: 1, minHeight: Math.round(height * 0.3), marginTop: pad, marginBottom: pad }} />
  ) : (
    <div style={{ display: "flex", flex: 1 }} />
  );
  if (tall || !photo) {
    return (
      <div style={{ ...canvas, flexDirection: "column" }}>
        {top}
        {middle}
        {bottom}
      </div>
    );
  }
  return (
    <div style={{ ...canvas, flexDirection: "row" }}>
      <div style={{ display: "flex", flexDirection: "column", width: Math.round(width * 0.55), flexShrink: 0, justifyContent: "space-between" }}>
        {top}
        {bottom}
      </div>
      <Photo photo={photo} style={{ flex: 1 }} />
    </div>
  );
}
