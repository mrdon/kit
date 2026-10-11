import React from "react";
import { FactsLine, Logo, Photo, Text, accents, fitTitle, font, grounds, logoWidth, weights, type TemplateProps } from "@poster/kit";

export const meta = {
  name: "Caption",
  description: "The photo is the poster; one thin strip at the foot names it, with the facts as a line and the logo at the right",
  photos: { min: 1, max: 1 },
  needs: ["eyebrow", "title"],
};

export default function Template({ content, format, photos, ground, accent }: TemplateProps) {
  const { width, height, safe } = format;
  const s = Math.min(width, height) / 1080;
  const tall = height >= width;
  const g = grounds[ground];
  const a = accents[accent];
  const inner = width - safe.left - safe.right;
  const lw = Math.round(logoWidth(s) * 0.9);
  const textW = inner - lw - Math.round(32 * s);
  const titleSize = fitTitle(content.title, tall ? textW : inner * 0.5, Math.round((tall ? 72 : 64) * s), tall ? 2 : 1);
  return (
    <div style={{ boxSizing: "border-box", display: "flex", flexDirection: "column", width, height, overflow: "hidden", backgroundColor: g.bg, color: g.text, fontFamily: font.text }}>
      <Photo photo={photos[0]} style={{ flex: 1 }} />
      <div
        style={{
          boxSizing: "border-box",
          display: "flex",
          flexShrink: 0,
          alignItems: "center",
          justifyContent: "space-between",
          gap: Math.round(32 * s),
          borderTop: `${Math.max(4, Math.round(6 * s))}px solid ${a.fill}`,
          padding: `${Math.round(32 * s)}px ${safe.right}px ${safe.bottom}px ${safe.left}px`,
        }}
      >
        <div style={{ display: "flex", flexDirection: "column", width: tall ? textW : inner * 0.5 }}>
          <span style={{ fontSize: Math.round(28 * s), fontWeight: weights.textBold, color: g.label, lineHeight: 1.2 }}>
            <Text>{content.eyebrow}</Text>
          </span>
          <h1 style={{ margin: `${Math.round(10 * s)}px 0 0`, fontFamily: font.display, fontWeight: weights.display, fontSize: titleSize, lineHeight: 0.98, letterSpacing: "-0.01em", whiteSpace: "pre-line", textWrap: "balance" }}>
            <Text>{content.title}</Text>
          </h1>
          {tall ? <FactsLine content={content} size={Math.round(26 * s)} bold={false} style={{ marginTop: Math.round(12 * s) }} /> : null}
        </div>
        {!tall ? <FactsLine content={content} size={Math.round(28 * s)} style={{ flex: 1, textAlign: "center" }} /> : null}
        <div style={{ display: "flex", flexShrink: 0 }}>
          <Logo variant={g.logo} width={lw} />
        </div>
      </div>
    </div>
  );
}
