import React from "react";
import { AccentBar, FactsLine, Logo, Photo, Text, fitTitle, font, grounds, logoWidth, otherGround, weights, type TemplateProps } from "@poster/kit";

export const meta = {
  name: "Badge",
  description: "A disc at the top, either the photo cropped round or the other ground holding the time, centred copy below, logo bottom-centre",
  photos: { min: 1, max: 1 },
  needs: ["eyebrow", "title"],
};

export default function Template({ content, format, photos, ground, accent }: TemplateProps) {
  const { width, height, safe } = format;
  const s = Math.min(width, height) / 1080;
  const tall = height >= width;
  const story = height > width * 1.5;
  const g = grounds[ground];
  const o = grounds[otherGround(ground)];
  const inner = width - safe.left - safe.right;
  const D = Math.round(tall ? inner * (story ? 0.6 : 0.6) : height * 0.58);
  const when = content.details.find((d) => /^when$/i.test(d.label));
  const photo = photos[0];
  const disc = (
    <div style={{ display: "flex", width: D, height: D, borderRadius: D / 2, overflow: "hidden", flexShrink: 0, backgroundColor: o.bg, color: o.text }}>
      {photo ? (
        <Photo photo={{ ...photo, zoom: Math.max(photo.zoom ?? 1, 1.3) }} style={{ width: D, height: D }} />
      ) : (
        <div style={{ display: "flex", flex: 1, flexDirection: "column", alignItems: "center", justifyContent: "center", textAlign: "center", padding: D * 0.1 }}>
          <span style={{ fontFamily: font.display, fontWeight: weights.display, fontSize: Math.round(D * 0.1), lineHeight: 1.1 }}>
            <Text>{content.eyebrow}</Text>
          </span>
          {when ? (
            <span style={{ fontFamily: font.display, fontWeight: weights.display, fontSize: Math.round(D * 0.17), lineHeight: 1.05, marginTop: D * 0.04, textWrap: "balance" }}>
              <Text>{when.value}</Text>
            </span>
          ) : null}
        </div>
      )}
    </div>
  );
  const copyW = tall ? inner * 0.9 : width * 0.46;
  const titleSize = fitTitle(content.title, copyW, Math.round((tall ? 130 : 120) * s), tall && !story ? 2 : 3);
  const copy = (
    <div style={{ display: "flex", flexDirection: "column", alignItems: tall ? "center" : "flex-start", textAlign: tall ? "center" : "left", width: tall ? inner : copyW, flex: tall ? 1 : undefined }}>
      <div style={{ display: "flex", flexDirection: "column", alignItems: tall ? "center" : "flex-start", justifyContent: "center", flex: tall ? undefined : 1 }}>
      <AccentBar accent={accent} width={Math.round(120 * s)} height={Math.round(12 * s)} style={{ marginTop: tall ? Math.round(44 * s) : 0 }} />
      <h1 style={{ margin: `${Math.round(28 * s)}px 0 0`, fontFamily: font.display, fontWeight: weights.display, fontSize: titleSize, lineHeight: 0.95, letterSpacing: "-0.01em", whiteSpace: "pre-line", textWrap: "balance", maxWidth: copyW }}>
        <Text>{content.title}</Text>
      </h1>
      {content.summary ? (
        <p style={{ margin: `${Math.round(22 * s)}px 0 0`, fontSize: Math.round(32 * s), lineHeight: 1.4, maxWidth: copyW * 0.9, textWrap: "balance" }}>
          <Text>{content.summary}</Text>
        </p>
      ) : null}
      <FactsLine content={content} skip={photo ? undefined : "When"} size={Math.round(30 * s)} color={g.label} style={{ marginTop: Math.round(24 * s), maxWidth: copyW }} />
      {content.action ? (
        <p style={{ margin: `${Math.round(12 * s)}px 0 0`, fontSize: Math.round(30 * s), fontWeight: weights.textBold, lineHeight: 1.3 }}>
          <Text>{content.action}</Text>
        </p>
      ) : null}
      </div>
      <div style={{ display: "flex", marginTop: "auto", paddingTop: Math.round(36 * s), alignSelf: tall ? "center" : "flex-end" }}>
        <Logo variant={g.logo} width={logoWidth(s)} />
      </div>
    </div>
  );
  const canvas = {
    boxSizing: "border-box",
    display: "flex",
    width,
    height,
    overflow: "hidden",
    backgroundColor: g.bg,
    color: g.text,
    fontFamily: font.text,
    padding: `${safe.top}px ${safe.right}px ${safe.bottom}px ${safe.left}px`,
  } as const;
  if (tall) {
    return (
      <div style={{ ...canvas, flexDirection: "column", alignItems: "center" }}>
        {disc}
        {copy}
      </div>
    );
  }
  return (
    <div style={{ ...canvas, flexDirection: "row", alignItems: "center", gap: Math.round(72 * s) }}>
      <div style={{ display: "flex", width: Math.round(width * 0.42), justifyContent: "center", flexShrink: 0 }}>{disc}</div>
      <div style={{ display: "flex", flex: 1, alignSelf: "stretch", flexDirection: "column" }}>{copy}</div>
    </div>
  );
}
