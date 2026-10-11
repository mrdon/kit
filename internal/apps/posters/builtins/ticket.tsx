import React from "react";
import { FactsLine, Logo, Photo, Text, accents, fitTitle, font, grounds, logoWidth, weights, type TemplateProps } from "@poster/kit";

export const meta = {
  name: "Ticket",
  description: "A photo panel with the title, and a stub in the accent colour down the side carrying the date and time like a ticket",
  photos: { min: 1, max: 1 },
  needs: ["eyebrow", "title"],
};

// "Sat Oct 24" -> ["Sat", "24", "Oct"]-ish pieces for the stub: words
// stacked, the day number set large.
function stubLines(eyebrow: string): { big?: string; small: string[] } {
  const words = eyebrow.trim().split(/\s+/);
  const i = words.findIndex((w) => /^\d{1,2}(st|nd|rd|th)?,?$/i.test(w));
  if (i < 0) return { small: words };
  return { big: words[i].replace(/\D/g, ""), small: words.filter((_, j) => j !== i) };
}

export default function Template({ content, format, photos, ground, accent }: TemplateProps) {
  const { width, height, safe } = format;
  const s = Math.min(width, height) / 1080;
  const tall = height >= width;
  const story = height > width * 1.5;
  const g = grounds[ground];
  const a = accents[accent];
  const pad = Math.round(48 * s);
  const when = content.details.find((d) => /^when$/i.test(d.label));
  const { big, small } = stubLines(content.eyebrow);
  const sideStub = tall && !story;
  const stubPadX = Math.max(Math.round(28 * s), safe.right);
  const stubW = Math.round(width * 0.3);
  const stubInner = stubW - stubPadX * 2;
  const dash = `${Math.max(2, Math.round(3 * s))}px dashed ${g.rule}`;
  const pieces = (
    <>
      {small.map((w) => (
        <span key={w} style={{ fontSize: Math.round(28 * s), fontWeight: weights.textBold, lineHeight: 1.1 }}>
          <Text>{w}</Text>
        </span>
      ))}
      {big ? (
        <span style={{ fontFamily: font.mono, fontWeight: weights.mono, fontSize: Math.round(sideStub ? Math.min(stubInner * 0.8, 170 * s) : 120 * s), lineHeight: 1 }}>{big}</span>
      ) : null}
      {when ? (
        <span style={{ fontSize: Math.round(32 * s), fontWeight: weights.textBold, lineHeight: 1.15, textWrap: "balance", maxWidth: sideStub ? stubInner : undefined }}>
          <Text>{when.value}</Text>
        </span>
      ) : null}
    </>
  );
  const action = content.action ? (
    <span style={{ fontSize: Math.round(26 * s), fontWeight: weights.textBold, lineHeight: 1.25, textWrap: "balance", maxWidth: sideStub ? stubInner : Math.round(width * 0.3) }}>
      <Text>{content.action}</Text>
    </span>
  ) : null;
  // Down the right on a portrait sheet; a band along the bottom otherwise.
  const stub = sideStub ? (
    <div style={{ boxSizing: "border-box", display: "flex", flexDirection: "column", justifyContent: "space-between", alignItems: "center", textAlign: "center", width: stubW, flexShrink: 0, backgroundColor: a.fill, color: a.text, padding: `${safe.top}px ${stubPadX}px ${safe.bottom}px`, borderLeft: dash }}>
      <div style={{ display: "flex", flexDirection: "column", alignItems: "center", gap: Math.round(10 * s) }}>{pieces}</div>
      {action}
    </div>
  ) : (
    <div style={{ boxSizing: "border-box", display: "flex", alignItems: "center", justifyContent: "space-between", gap: Math.round(32 * s), flexShrink: 0, backgroundColor: a.fill, color: a.text, padding: `${Math.round(28 * s)}px ${safe.right}px ${Math.max(safe.bottom, Math.round(28 * s))}px ${safe.left}px`, borderTop: dash }}>
      <div style={{ display: "flex", alignItems: "center", gap: Math.round(20 * s) }}>{pieces}</div>
      {action}
    </div>
  );
  const mainW = sideStub ? width - stubW : width;
  const copyPadR = sideStub ? pad : safe.right;
  const copyW = tall ? mainW - safe.left - copyPadR : Math.round(width * 0.46);
  const titleSize = fitTitle(content.title, copyW, Math.round((tall ? 120 : 110) * s), 2);
  const copy = (
    <div style={{ boxSizing: "border-box", display: "flex", flexDirection: "column", flex: 1, padding: tall ? `${pad}px ${copyPadR}px ${sideStub ? safe.bottom : pad}px ${safe.left}px` : `${safe.top}px ${pad}px ${pad}px ${safe.left}px` }}>
      <h1 style={{ margin: 0, fontFamily: font.display, fontWeight: weights.display, fontSize: titleSize, lineHeight: 0.95, letterSpacing: "-0.01em", whiteSpace: "pre-line", textWrap: "balance", maxWidth: copyW }}>
        <Text>{content.title}</Text>
      </h1>
      {content.summary ? (
        <p style={{ margin: `${Math.round(20 * s)}px 0 0`, fontSize: Math.round(30 * s), lineHeight: 1.4, maxWidth: copyW * 0.95, textWrap: "balance" }}>
          <Text>{content.summary}</Text>
        </p>
      ) : null}
      <div style={{ display: "flex", alignItems: "flex-end", justifyContent: "space-between", gap: Math.round(24 * s), marginTop: "auto", paddingTop: Math.round(24 * s) }}>
        <FactsLine content={content} skip={when?.label} size={Math.round(28 * s)} color={g.label} />
        <div style={{ display: "flex", flexShrink: 0 }}>
          <Logo variant={g.logo} width={Math.round(logoWidth(s) * 0.9)} />
        </div>
      </div>
    </div>
  );
  const canvas = { boxSizing: "border-box", display: "flex", width, height, overflow: "hidden", backgroundColor: g.bg, color: g.text, fontFamily: font.text } as const;
  if (sideStub) {
    return (
      <div style={{ ...canvas, flexDirection: "row" }}>
        <div style={{ display: "flex", flexDirection: "column", width: mainW }}>
          <Photo photo={photos[0]} style={{ height: Math.round(height * 0.5), flexShrink: 0 }} />
          {copy}
        </div>
        {stub}
      </div>
    );
  }
  if (story) {
    return (
      <div style={{ ...canvas, flexDirection: "column" }}>
        <Photo photo={photos[0]} style={{ height: Math.round(height * 0.44), flexShrink: 0 }} />
        {copy}
        {stub}
      </div>
    );
  }
  return (
    <div style={{ ...canvas, flexDirection: "row" }}>
      <Photo photo={photos[0]} style={{ width: Math.round(width * 0.46), flexShrink: 0 }} />
      <div style={{ display: "flex", flexDirection: "column", flex: 1 }}>
        {copy}
        {stub}
      </div>
    </div>
  );
}
