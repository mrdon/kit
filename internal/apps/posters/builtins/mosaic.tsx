import React from "react";
import { Eyebrow, FactsLine, Logo, Photo, Text, fitTitle, font, grounds, logoWidth, weights, type TemplateProps } from "@poster/kit";

export const meta = {
  name: "Mosaic",
  description: "A two-by-two grid: three photos and one tile of copy on the ground, so the event reads as a set of moments",
  photos: { min: 3, max: 3 },
  needs: ["eyebrow", "title"],
};

export default function Template({ content, format, photos, ground, accent }: TemplateProps) {
  const { width, height, safe } = format;
  const s = Math.min(width, height) / 1080;
  const tall = height >= width;
  const g = grounds[ground];
  const gap = Math.round(12 * s);
  const cellW = Math.round((width - gap) / 2);
  const cellH = Math.round((height - gap) / 2);
  const padX = Math.round(Math.max(safe.left, 44 * s));
  const copyW = cellW - padX - Math.round(32 * s);
  const titleSize = fitTitle(content.title, copyW, Math.round((tall ? 96 : 110) * s), 3);
  const copy = (
    <div style={{ boxSizing: "border-box", display: "flex", flexDirection: "column", width: cellW, height: cellH, padding: `${Math.round(Math.max(safe.top, 44 * s))}px ${Math.round(32 * s)}px ${Math.round(Math.max(safe.bottom, 44 * s))}px ${padX}px`, backgroundColor: g.bg, color: g.text }}>
      <Eyebrow content={content} accent={accent} s={s * 0.85} />
      <h1 style={{ margin: `${Math.round(22 * s)}px 0 0`, fontFamily: font.display, fontWeight: weights.display, fontSize: titleSize, lineHeight: 0.95, letterSpacing: "-0.01em", whiteSpace: "pre-line", textWrap: "balance", maxWidth: copyW }}>
        <Text>{content.title}</Text>
      </h1>
      <FactsLine content={content} size={Math.round(26 * s)} style={{ marginTop: Math.round(18 * s), maxWidth: copyW }} />
      {content.action ? (
        <span style={{ marginTop: Math.round(8 * s), fontSize: Math.round(24 * s), fontWeight: weights.textBold, color: g.label, lineHeight: 1.3 }}>
          <Text>{content.action}</Text>
        </span>
      ) : null}
      <div style={{ display: "flex", marginTop: "auto", paddingTop: Math.round(20 * s) }}>
        <Logo variant={g.logo} width={Math.round(logoWidth(s) * 0.85)} />
      </div>
    </div>
  );
  // Tall canvases put the copy bottom-left so the eye lands on photos first;
  // wide ones put it top-left, where a screen is read from.
  const cells = tall ? [photos[0], photos[1], null, photos[2]] : [null, photos[0], photos[1], photos[2]];
  return (
    <div style={{ boxSizing: "border-box", display: "flex", flexWrap: "wrap", gap, width, height, overflow: "hidden", backgroundColor: g.bg, color: g.text, fontFamily: font.text }}>
      {cells.map((p, i) => (p ? <Photo key={i} photo={p} style={{ width: cellW, height: cellH }} /> : <React.Fragment key={i}>{copy}</React.Fragment>))}
    </div>
  );
}
