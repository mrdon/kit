import React from "react";
import { CopyHead, Logo, Photo, Text, font, grounds, logoWidth, otherGround, weights, type TemplateProps } from "@poster/kit";

export const meta = {
  name: "Twotone",
  description: "Copy on one ground, a full-width band in the brand's other ground carrying the facts and the logo; the photo sits between them",
  photos: { min: 0, max: 1 },
  needs: ["eyebrow", "title"],
};

export default function Template({ content, format, photos, ground, accent }: TemplateProps) {
  const { width, height, safe } = format;
  const s = Math.min(width, height) / 1080;
  const tall = height >= width;
  const pad = Math.round(56 * s);
  const inner = width - safe.left - safe.right;
  const bName = otherGround(ground);
  const b = grounds[bName];
  const g = grounds[ground];
  const photo = photos[0];
  const band = (
    <div
      style={{
        boxSizing: "border-box",
        display: "flex",
        flexShrink: 0,
        alignItems: "flex-end",
        justifyContent: "space-between",
        gap: Math.round(40 * s),
        minHeight: Math.round(height * (tall ? 0.14 : 0.3)),
        backgroundColor: b.bg,
        color: b.text,
        fontFamily: font.text,
        padding: `${pad}px ${safe.right}px ${safe.bottom}px ${safe.left}px`,
        borderTop: bName === ground ? `${Math.max(2, Math.round(3 * s))}px solid ${g.rule}` : undefined,
      }}
    >
      <div style={{ display: "flex", flexDirection: "column", gap: Math.round(14 * s) }}>
        {content.details.map((d) => (
          <div key={d.label} style={{ display: "flex", flexDirection: "column" }}>
            <span style={{ fontSize: Math.round(24 * s), fontWeight: weights.textBold, color: b.label }}>{d.label}</span>
            <span style={{ fontSize: Math.round(32 * s), lineHeight: 1.25 }}>
              <Text>{d.value}</Text>
            </span>
          </div>
        ))}
        {content.action ? (
          <span style={{ fontSize: Math.round(30 * s), fontWeight: weights.textBold, lineHeight: 1.3, marginTop: Math.round(6 * s) }}>
            <Text>{content.action}</Text>
          </span>
        ) : null}
      </div>
      <div style={{ display: "flex", flexShrink: 0 }}>
        <Logo variant={b.logo} width={logoWidth(s)} />
      </div>
    </div>
  );
  const canvas = { boxSizing: "border-box", display: "flex", width, height, overflow: "hidden", backgroundColor: g.bg, color: g.text, fontFamily: font.text } as const;
  if (tall) {
    return (
      <div style={{ ...canvas, flexDirection: "column" }}>
        <CopyHead
          content={content}
          ground={ground}
          accent={accent}
          s={s}
          width={inner}
          titleMax={Math.round((photo ? 130 : 170) * s)}
          titleLines={photo ? 2 : 4}
          style={{ padding: `${safe.top}px ${safe.right}px ${pad}px ${safe.left}px`, flexShrink: 0, flex: photo ? undefined : 1 }}
        />
        {photo ? <Photo photo={photo} style={{ flex: 1, minHeight: Math.round(height * 0.2) }} /> : null}
        {band}
      </div>
    );
  }
  const leftW = photo ? Math.round(width * 0.56) : width;
  return (
    <div style={{ ...canvas, flexDirection: "row" }}>
      <div style={{ display: "flex", flexDirection: "column", width: leftW, flexShrink: 0 }}>
        <CopyHead
          content={content}
          ground={ground}
          accent={accent}
          s={s}
          width={leftW - safe.left - pad}
          titleMax={Math.round(120 * s)}
          titleLines={3}
          style={{ padding: `${safe.top}px ${pad}px ${pad}px ${safe.left}px`, flex: 1 }}
        />
        {band}
      </div>
      {photo ? <Photo photo={photo} style={{ flex: 1 }} /> : null}
    </div>
  );
}
