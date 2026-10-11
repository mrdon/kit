import React from "react";
import { Eyebrow, Logo, Text, fitTitle, font, grounds, logoWidth, weights, type TemplateProps } from "@poster/kit";

export const meta = {
  name: "Masthead",
  description: "No photo: the logo tops the sheet like a print poster, the biggest title in the set, the facts in a ruled row at the foot",
  photos: { min: 0, max: 0 },
  needs: ["eyebrow", "title"],
};

export default function Template({ content, format, ground, accent }: TemplateProps) {
  const { width, height, safe } = format;
  const s = Math.min(width, height) / 1080;
  const tall = height >= width;
  const story = height > width * 1.5;
  const g = grounds[ground];
  const inner = width - safe.left - safe.right;
  const rowW = tall ? inner : Math.round(inner * 0.8);
  const titleSize = fitTitle(content.title, tall ? inner : inner * 0.85, Math.round((tall ? 200 : 150) * s), tall ? 4 : 2);
  const rule = `${Math.max(2, Math.round(3 * s))}px solid ${g.rule}`;
  const details = content.details.slice(0, 3);
  return (
    <div
      style={{
        boxSizing: "border-box",
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        textAlign: "center",
        width,
        height,
        overflow: "hidden",
        backgroundColor: g.bg,
        color: g.text,
        fontFamily: font.text,
        padding: `${safe.top}px ${safe.right}px ${safe.bottom}px ${safe.left}px`,
      }}
    >
      <Logo variant={g.logo} width={Math.round(logoWidth(s) * (story ? 1.4 : tall ? 1.2 : 1))} />
      <div style={{ display: "flex", marginTop: Math.round(36 * s) }}>
        <Eyebrow content={content} accent={accent} s={s} />
      </div>
      <h1
        style={{
          margin: `${Math.round(36 * s)}px 0 0`,
          fontFamily: font.display,
          fontWeight: weights.display,
          fontSize: titleSize,
          lineHeight: 0.92,
          letterSpacing: "-0.015em",
          whiteSpace: "pre-line",
          textWrap: "balance",
          maxWidth: tall ? inner : inner * 0.85,
        }}
      >
        <Text>{content.title}</Text>
      </h1>
      {content.summary ? (
        <p style={{ margin: `${Math.round(32 * s)}px 0 0`, fontSize: Math.round(34 * s), lineHeight: 1.4, maxWidth: inner * 0.8, textWrap: "balance" }}>
          <Text>{content.summary}</Text>
        </p>
      ) : null}
      <div style={{ display: "flex", flex: 1 }} />
      {details.length ? (
        <div style={{ display: "flex", width: rowW, borderTop: rule, borderBottom: rule, padding: `${Math.round(28 * s)}px 0`, gap: Math.round(40 * s) }}>
          {details.map((d) => (
            <div key={d.label} style={{ display: "flex", flex: 1, flexDirection: "column", alignItems: "center", gap: Math.round(8 * s) }}>
              <span style={{ fontSize: Math.round(24 * s), fontWeight: weights.textBold, color: g.label }}>{d.label}</span>
              <span style={{ fontSize: Math.round((details.length === 3 ? 30 : 36) * s), lineHeight: 1.2 }}>
                <Text>{d.value}</Text>
              </span>
            </div>
          ))}
        </div>
      ) : null}
      {content.action ? (
        <p style={{ margin: `${Math.round(28 * s)}px 0 0`, fontSize: Math.round(32 * s), fontWeight: weights.textBold, lineHeight: 1.3 }}>
          <Text>{content.action}</Text>
        </p>
      ) : null}
    </div>
  );
}
