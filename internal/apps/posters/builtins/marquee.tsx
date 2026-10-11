import React from "react";
import { Eyebrow, FactsLine, Logo, Photo, Text, fitTitle, font, grounds, logoWidth, weights, type TemplateProps } from "@poster/kit";

export const meta = {
  name: "Marquee",
  description: "Photo on top, a centred panel of copy below, logo bottom-centre: the taproom poster, the facts as one line",
  prompt: "The everyday taproom poster: photo on top, centred copy below, facts as one line. Works for almost any event with one decent photo. Prefer a landscape photo with the subject in the middle. Put the time and the one important fact in details.",
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
    boxSizing: "border-box",
    display: "flex",
    flexDirection: tall ? "column" : "row",
    width,
    height,
    overflow: "hidden",
    backgroundColor: g.bg,
    color: g.text,
    fontFamily: font.text,
  } as const;
  const panelW = tall ? inner : Math.round(width * 0.5 - safe.right - pad);
  const titleSize = fitTitle(content.title, panelW * 0.92, Math.round((tall ? 124 : 110) * s), 2);
  // The panel has room for a long summary or for two details, not both;
  // the facts line wins, since that is what a poster is for.
  const summary = content.summary && !(tall && content.details.length >= 2 && content.summary.length > 90) ? content.summary : "";
  const panel = (
    <div
      style={{
        boxSizing: "border-box",
        display: "flex",
        flex: 1,
        flexDirection: "column",
        alignItems: "center",
        justifyContent: tall ? "flex-start" : "center",
        textAlign: "center",
        padding: tall ? `${pad}px ${safe.right}px ${safe.bottom}px ${safe.left}px` : `${safe.top}px ${safe.right}px ${safe.bottom}px ${pad}px`,
      }}
    >
      <div style={{ display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", flex: tall ? undefined : 1 }}>
      <Eyebrow content={content} accent={accent} s={s} />
      <h1
        style={{
          margin: `${Math.round(28 * s)}px 0 0`,
          fontFamily: font.display,
          fontWeight: weights.display,
          fontSize: titleSize,
          lineHeight: 0.95,
          letterSpacing: "-0.01em",
          whiteSpace: "pre-line",
          textWrap: "balance",
          maxWidth: panelW * 0.92,
        }}
      >
        <Text>{content.title}</Text>
      </h1>
      {summary ? (
        <p style={{ margin: `${Math.round(20 * s)}px 0 0`, fontSize: Math.round(30 * s), lineHeight: 1.35, maxWidth: panelW * 0.8, textWrap: "balance" }}>
          <Text>{summary}</Text>
        </p>
      ) : null}
      <FactsLine content={content} size={Math.round(32 * s)} style={{ marginTop: Math.round(28 * s), maxWidth: panelW * 0.9 }} />
      {content.action ? (
        <p style={{ margin: `${Math.round(14 * s)}px 0 0`, fontSize: Math.round(28 * s), lineHeight: 1.35, color: g.label, fontWeight: weights.textBold }}>
          <Text>{content.action}</Text>
        </p>
      ) : null}
      </div>
      <div style={{ display: "flex", marginTop: "auto", paddingTop: Math.round(28 * s), flexShrink: 0 }}>
        <Logo variant={g.logo} width={Math.round(logoWidth(s) * 0.8)} />
      </div>
    </div>
  );
  if (tall) {
    return (
      <div style={canvas}>
        <Photo photo={photos[0]} style={{ height: Math.round(height * (height > width * 1.5 ? 0.46 : 0.44)), flexShrink: 0 }} />
        {panel}
      </div>
    );
  }
  return (
    <div style={canvas}>
      <Photo photo={photos[0]} style={{ width: Math.round(width * 0.5), flexShrink: 0 }} />
      {panel}
    </div>
  );
}
