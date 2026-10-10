import React from "react";
import { accents, displayAdvance, font, grounds, logoAspects, weights, type Format } from "./brand";

// The poster kit: the only building blocks a poster or template may use.
// Every number is set in mono, every color comes from a ground or accent,
// and images are references the host resolves. A layout composes these; it
// cannot draw.

type Style = Record<string, string | number | undefined>;

export type Detail = { label: string; value: string };
export type Content = {
  eyebrow: string;
  title: string;
  summary?: string;
  details: Detail[];
  action?: string;
};
export type PhotoUse = { id: string; focusX?: number; focusY?: number; zoom?: number };
export type TemplateProps = {
  content: Content;
  format: Format;
  photos: PhotoUse[];
  ground: string;
  accent: string;
};

const NUMERIC = /(\$?\d[\d:.,]*%?)/g;

// Sets any run of digits (with its $, %, :) in the mono family and leaves
// the words in whatever family the parent set. "9pm" is mono 9, text pm.
export const Text = ({ children }: { children: unknown }) => {
  const text = Array.isArray(children) ? children.join("") : String(children ?? "");
  const parts = text.split(NUMERIC);
  if (parts.length === 1) return text;
  return (
    <span>
      {parts.map((part, i) =>
        i % 2 === 1 ? (
          <span
            key={i}
            style={{
              fontFamily: font.mono,
              fontWeight: weights.mono,
              fontVariantNumeric: "tabular-nums",
              fontSize: "0.9em",
              letterSpacing: 0,
            }}
          >
            {part}
          </span>
        ) : (
          part
        ),
      )}
    </span>
  );
};

const lineCount = (words: string[], charsPerLine: number) => {
  let lines = 1;
  let used = 0;
  for (const w of words) {
    if (w.length > charsPerLine) return Infinity;
    const need = used === 0 ? w.length : used + 1 + w.length;
    if (need > charsPerLine) {
      lines++;
      used = w.length;
    } else {
      used = need;
    }
  }
  return lines;
};

// The largest display size at which a title fits its box in at most
// maxLines lines, by average advance width.
export const fitTitle = (title: string, width: number, maxSize: number, maxLines: number) => {
  const forced = title.split("\n");
  const limit = forced.length > 1 ? forced.length : maxLines;
  for (let size = maxSize; size > 12; size -= 2) {
    const perLine = Math.floor(width / (size * displayAdvance));
    const lines = forced.reduce((n, l) => n + lineCount(l.trim().split(/\s+/), perLine), 0);
    if (lines <= limit) return size;
  }
  return 12;
};

// A library photo filling its box, cropped around its focus point. The host
// resolves the id; an unknown id fails validation.
export const Photo = ({ photo, id, focusX, focusY, zoom, style }: {
  photo?: PhotoUse;
  id?: string;
  focusX?: number;
  focusY?: number;
  zoom?: number;
  style?: Style;
}) => {
  const use = photo ?? { id: id ?? "", focusX, focusY, zoom };
  return (
    <div style={{ position: "relative", boxSizing: "border-box", display: "flex", overflow: "hidden", ...style }}>
      {React.createElement("__photo", {
        id: use.id,
        focusX: use.focusX ?? 0.5,
        focusY: use.focusY ?? 0.5,
        zoom: use.zoom ?? 1,
      })}
    </div>
  );
};

// The logo, at a width, in the variant the ground needs. A variant the
// tenant has not mapped leaves its space empty: the guide forbids drawing a
// substitute.
export const Logo = ({ variant, width }: { variant: string; width: number }) => {
  const aspect = logoAspects[variant];
  const height = Math.round(width / (aspect || 1));
  // The host draws the file, or an empty box of the same size when the
  // variant is unmapped (and says so in the render's warnings).
  return React.createElement("__logo", { key: "logo", variant, width, height });
};

export const logoWidth = (s: number) => Math.max(140, Math.round(150 * s));

type Shared = { content: Content; ground: string; accent: string; s: number; style?: Style };

const base = (ground: string): Style => ({
  boxSizing: "border-box",
  display: "flex",
  flexDirection: "column",
  color: grounds[ground].text,
  fontFamily: font.text,
});

// The one accent element: a short line (usually the date) on the accent fill.
export const Eyebrow = ({ content, accent, s }: { content: Content; accent: string; s: number }) => {
  const a = accents[accent];
  return (
    <div
      style={{
        display: "flex",
        alignSelf: "flex-start",
        backgroundColor: a.fill,
        color: a.text,
        fontFamily: font.text,
        fontWeight: weights.textBold,
        fontSize: Math.round(32 * s),
        lineHeight: 1,
        padding: `${Math.round(14 * s)}px ${Math.round(20 * s)}px`,
      }}
    >
      <Text>{content.eyebrow}</Text>
    </div>
  );
};

// Eyebrow, title and summary.
export const CopyHead = ({ content, ground, accent, s, width, titleMax, titleLines, eyebrow = true, style }: Shared & {
  width: number;
  titleMax: number;
  titleLines: number;
  eyebrow?: boolean;
}) => {
  const titleSize = fitTitle(content.title, width, titleMax, titleLines);
  return (
    <div style={{ ...base(ground), ...style }}>
      {eyebrow ? <Eyebrow content={content} accent={accent} s={s} /> : null}
      <h1
        style={{
          margin: `${eyebrow ? Math.round(32 * s) : 0}px 0 0`,
          fontFamily: font.display,
          fontWeight: weights.display,
          fontSize: titleSize,
          lineHeight: 0.95,
          letterSpacing: "-0.01em",
          whiteSpace: "pre-line",
          textWrap: "balance",
          maxWidth: width,
        }}
      >
        <Text>{content.title}</Text>
      </h1>
      {content.summary ? (
        <p
          style={{
            margin: `${Math.round(28 * s)}px 0 0`,
            fontSize: Math.round(34 * s),
            lineHeight: 1.4,
            textWrap: "balance",
            maxWidth: Math.min(width * 0.9, 34 * s * 0.5 * 65),
          }}
        >
          <Text>{content.summary}</Text>
        </p>
      ) : null}
    </div>
  );
};

// Details, action and the logo in the bottom corner.
export const CopyFoot = ({ content, ground, s, showLogo = true, skipDetail, style }: Shared & {
  showLogo?: boolean;
  skipDetail?: string;
}) => {
  const g = grounds[ground];
  const lw = logoWidth(s);
  const clear = Math.round(lw * 0.17);
  const details = content.details.filter((d) => d.label !== skipDetail);
  return (
    <div
      style={{
        ...base(ground),
        flexDirection: "row",
        alignItems: "flex-end",
        justifyContent: "space-between",
        gap: clear,
        ...style,
      }}
    >
      <div style={{ display: "flex", flexDirection: "column", gap: Math.round(10 * s) }}>
        {details.map((d) => (
          <div key={d.label} style={{ display: "flex", alignItems: "baseline", fontSize: Math.round(32 * s) }}>
            <span
              style={{
                width: Math.round(130 * s),
                flexShrink: 0,
                fontWeight: weights.textBold,
                fontSize: Math.round(26 * s),
                color: g.label,
              }}
            >
              {d.label}
            </span>
            <span style={{ lineHeight: 1.3 }}>
              <Text>{d.value}</Text>
            </span>
          </div>
        ))}
        {content.action ? (
          <div
            style={{
              display: "flex",
              marginTop: details.length ? Math.round(18 * s) : 0,
              paddingTop: details.length ? Math.round(18 * s) : 0,
              borderTop: details.length ? `${Math.max(2, Math.round(2 * s))}px solid ${g.rule}` : undefined,
              fontWeight: weights.textBold,
              fontSize: Math.round(32 * s),
              lineHeight: 1.3,
            }}
          >
            <Text>{content.action}</Text>
          </div>
        ) : null}
      </div>
      {showLogo ? (
        <div style={{ display: "flex", flexShrink: 0 }}>
          <Logo variant={g.logo} width={lw} />
        </div>
      ) : null}
    </div>
  );
};

// Head and foot stacked, foot pushed to the bottom.
export const CopyBlock = ({ content, ground, accent, s, width, titleMax, titleLines, showLogo = true, style }: Shared & {
  width: number;
  titleMax: number;
  titleLines: number;
  showLogo?: boolean;
}) => (
  <div style={{ position: "absolute", ...base(ground), ...style }}>
    <CopyHead content={content} ground={ground} accent={accent} s={s} width={width} titleMax={titleMax} titleLines={titleLines} />
    <CopyFoot
      content={content}
      ground={ground}
      accent={accent}
      s={s}
      showLogo={showLogo}
      style={{ marginTop: "auto", paddingTop: Math.round(32 * s) }}
    />
  </div>
);
