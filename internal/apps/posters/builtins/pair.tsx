import React from "react";
import { AccentBar, FactsLine, Logo, Photo, Text, fitTitle, font, grounds, logoWidth, weights, type TemplateProps } from "@poster/kit";

export const meta = {
  name: "Pair",
  description: "Two photos side by side, a wide one and a narrow one, copy underneath with a short accent bar",
  prompt: "Two photos side by side, a wide one and a narrow one: use when two subjects tell the story (the band and the room, the beer and the food). The first photo is the wide one. Copy sits under them with room for a summary.",
  photos: { min: 2, max: 2 },
  needs: ["eyebrow", "title"],
};

export default function Template({ content, format, photos, ground, accent }: TemplateProps) {
  const { width, height, safe } = format;
  const s = Math.min(width, height) / 1080;
  const tall = height >= width;
  const story = height > width * 1.5;
  const g = grounds[ground];
  const pad = Math.round(56 * s);
  const inner = width - safe.left - safe.right;
  const gap = Math.round(12 * s);
  const copyW = tall ? inner : Math.round(width * 0.45 - safe.right - pad);
  const titleSize = fitTitle(content.title, copyW, Math.round((tall ? 140 : 120) * s), tall ? 2 : 3);
  const copy = (
    <div style={{ boxSizing: "border-box", display: "flex", flex: 1, flexDirection: "column", padding: tall ? `${pad}px ${safe.right}px ${safe.bottom}px ${safe.left}px` : `${safe.top}px ${safe.right}px ${safe.bottom}px ${pad}px` }}>
      <span style={{ fontSize: Math.round(30 * s), fontWeight: weights.textBold, color: g.label, lineHeight: 1.2 }}>
        <Text>{content.eyebrow}</Text>
      </span>
      <AccentBar accent={accent} width={Math.round(96 * s)} height={Math.round(12 * s)} style={{ marginTop: Math.round(16 * s) }} />
      <h1 style={{ margin: `${Math.round(24 * s)}px 0 0`, fontFamily: font.display, fontWeight: weights.display, fontSize: titleSize, lineHeight: 0.95, letterSpacing: "-0.01em", whiteSpace: "pre-line", textWrap: "balance", maxWidth: copyW }}>
        <Text>{content.title}</Text>
      </h1>
      {content.summary ? (
        <p style={{ margin: `${Math.round(22 * s)}px 0 0`, fontSize: Math.round(31 * s), lineHeight: 1.4, maxWidth: copyW * 0.92, textWrap: "balance" }}>
          <Text>{content.summary}</Text>
        </p>
      ) : null}
      <div style={{ display: "flex", alignItems: "flex-end", justifyContent: "space-between", gap: Math.round(32 * s), marginTop: "auto", paddingTop: Math.round(28 * s) }}>
        <div style={{ display: "flex", flexDirection: "column", gap: Math.round(8 * s) }}>
          <FactsLine content={content} size={Math.round(30 * s)} />
          {content.action ? (
            <span style={{ fontSize: Math.round(28 * s), fontWeight: weights.textBold, color: g.label, lineHeight: 1.3 }}>
              <Text>{content.action}</Text>
            </span>
          ) : null}
        </div>
        <div style={{ display: "flex", flexShrink: 0 }}>
          <Logo variant={g.logo} width={logoWidth(s)} />
        </div>
      </div>
    </div>
  );
  const canvas = { boxSizing: "border-box", display: "flex", width, height, overflow: "hidden", backgroundColor: g.bg, color: g.text, fontFamily: font.text } as const;
  if (tall) {
    const photoH = Math.round(height * (story ? 0.52 : 0.46));
    return (
      <div style={{ ...canvas, flexDirection: "column" }}>
        <div style={{ display: "flex", height: photoH, flexShrink: 0, gap }}>
          <Photo photo={photos[0]} style={{ width: Math.round((width - gap) * 0.6) }} />
          <Photo photo={photos[1]} style={{ flex: 1 }} />
        </div>
        {copy}
      </div>
    );
  }
  return (
    <div style={{ ...canvas, flexDirection: "row" }}>
      <div style={{ display: "flex", flexDirection: "column", width: Math.round(width * 0.55), flexShrink: 0, gap }}>
        <Photo photo={photos[0]} style={{ height: Math.round((height - gap) * 0.6) }} />
        <Photo photo={photos[1]} style={{ flex: 1 }} />
      </div>
      {copy}
    </div>
  );
}
