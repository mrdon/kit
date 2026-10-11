import React from "react";
import { CopyFoot, Photo, Text, accents, fitTitle, font, grounds, weights, type TemplateProps } from "@poster/kit";

export const meta = {
  name: "Calendar",
  description: "A big day-number tile in the accent colour with the title beside it; the photo, when there is one, runs underneath",
  prompt: "Use when the date is the draw: a one-off night, a release, a watch party. The tile shows the day number from the eyebrow, or the time for a weekly cadence. The photo runs under the headline, so pick one that reads at a wide, short crop.",
  photos: { min: 1, max: 1 },
  needs: ["eyebrow", "title"],
};

// "Sat Oct 24" -> { day: "24", caption: "Sat Oct" }; an eyebrow with no day
// number fills the tile with its words instead.
function splitDay(eyebrow: string): { day?: string; caption: string } {
  const words = eyebrow.trim().split(/\s+/);
  const i = words.findIndex((w) => /^\d{1,2}(st|nd|rd|th)?,?$/i.test(w));
  if (i < 0) return { caption: eyebrow };
  const day = words[i].replace(/\D/g, "");
  return { day, caption: words.filter((_, j) => j !== i).join(" ") };
}

export default function Template({ content, format, photos, ground, accent }: TemplateProps) {
  const { width, height, safe } = format;
  const s = Math.min(width, height) / 1080;
  const tall = height >= width;
  const story = height > width * 1.5;
  const g = grounds[ground];
  const a = accents[accent];
  const pad = Math.round(56 * s);
  const inner = width - safe.left - safe.right;
  const photo = photos[0];
  const T = Math.round(tall ? inner * (story ? 0.38 : 0.42) : height * 0.46);
  const { day, caption } = splitDay(content.eyebrow);
  // A cadence ("Every Wednesday") has no day number; the time is the big
  // figure then, and the whole When detail moves out of the foot.
  const when = content.details.find((d) => /^when$/i.test(d.label));
  const big = day ?? when?.value;
  const bigSize = day ? T * 0.6 : fitTitle(when?.value ?? "", T * 0.84, Math.round(T * 0.26), 2);
  const tile = (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        justifyContent: "center",
        textAlign: "center",
        width: T,
        height: T,
        flexShrink: 0,
        backgroundColor: a.fill,
        color: a.text,
        padding: Math.round(T * 0.08),
      }}
    >
      <span style={{ fontFamily: font.text, fontWeight: weights.textBold, fontSize: Math.round(T * (big ? 0.1 : 0.14)), lineHeight: 1.15, textWrap: "balance" }}>
        <Text>{caption}</Text>
      </span>
      {big ? (
        <span style={{ fontFamily: day ? font.mono : font.display, fontWeight: day ? weights.mono : weights.display, fontSize: Math.round(bigSize), lineHeight: 0.95, marginTop: Math.round(T * 0.04), textWrap: "balance" }}>
          <Text>{big}</Text>
        </span>
      ) : null}
    </div>
  );
  const titleW = tall ? inner - T - Math.round(40 * s) : Math.round(width * 0.38);
  const titleSize = fitTitle(content.title, titleW, Math.round((photo ? 110 : 150) * s), tall ? 3 : 3);
  const title = (
    <div style={{ display: "flex", flexDirection: "column", justifyContent: "flex-end", width: titleW }}>
      <h1 style={{ margin: 0, fontFamily: font.display, fontWeight: weights.display, fontSize: titleSize, lineHeight: 0.95, letterSpacing: "-0.01em", whiteSpace: "pre-line", textWrap: "balance" }}>
        <Text>{content.title}</Text>
      </h1>
    </div>
  );
  const summary = content.summary ? (
    <p style={{ margin: `${Math.round(28 * s)}px 0 0`, fontSize: Math.round(30 * s), lineHeight: 1.35, maxWidth: Math.min(inner, 30 * s * 0.5 * 60), textWrap: "balance" }}>
      <Text>{content.summary}</Text>
    </p>
  ) : null;
  const canvas = { boxSizing: "border-box", display: "flex", width, height, overflow: "hidden", backgroundColor: g.bg, color: g.text, fontFamily: font.text } as const;
  const skip = day ? undefined : when?.label;
  const foot = <CopyFoot content={content} ground={ground} accent={accent} s={s} skipDetail={skip} style={{ padding: `${pad}px ${safe.right}px ${safe.bottom}px ${safe.left}px`, flexShrink: 0 }} />;
  if (tall) {
    return (
      <div style={{ ...canvas, flexDirection: "column" }}>
        <div style={{ display: "flex", flexDirection: "column", padding: `${safe.top}px ${safe.right}px ${pad}px ${safe.left}px`, flexShrink: 0 }}>
          <div style={{ display: "flex", alignItems: "flex-end", gap: Math.round(40 * s) }}>
            {tile}
            {title}
          </div>
          {summary}
        </div>
        {photo ? <Photo photo={photo} style={{ flex: 1, minHeight: Math.round(height * 0.18) }} /> : <div style={{ display: "flex", flex: 1 }} />}
        {foot}
      </div>
    );
  }
  return (
    <div style={{ ...canvas, flexDirection: "row" }}>
      <div style={{ display: "flex", flex: 1, flexDirection: "column", padding: `${safe.top}px ${pad}px ${safe.bottom}px ${safe.left}px` }}>
        <div style={{ display: "flex", alignItems: "flex-start", gap: Math.round(48 * s) }}>
          {tile}
          <div style={{ display: "flex", flexDirection: "column" }}>
            {title}
            {summary}
          </div>
        </div>
        <CopyFoot content={content} ground={ground} accent={accent} s={s} skipDetail={skip} style={{ marginTop: "auto", paddingTop: pad }} />
      </div>
      {photo ? <Photo photo={photo} style={{ width: Math.round(width * 0.36), flexShrink: 0 }} /> : null}
    </div>
  );
}
