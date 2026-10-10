// A numbered grid of thumbnails in one PNG: the cheap way for a model to
// look at a dozen library photos at once when writing their descriptions.
import { Resvg } from "@resvg/resvg-js";
import satori from "satori";
import sharp from "sharp";
import type { SatoriFont } from "./fonts.ts";
import type { ImageStore } from "./images.ts";
import type { ImageRef } from "./types.ts";

export const SHEET_MAX = 12;
const COLS = 4;
const WIDTH = 1920;
const PAD = 24;
const GAP = 20;
const LABEL = 20;
const CELL_W = Math.floor((WIDTH - PAD * 2 - GAP * (COLS - 1)) / COLS);
const CELL_H = 440;
const THUMB_H = CELL_H - LABEL - 12;

export type SheetItem = { image: ImageRef; label: string };

export async function renderSheet(items: SheetItem[], store: ImageStore, font: SatoriFont, bg: string, fg: string): Promise<Buffer> {
  const rows = Math.max(1, Math.ceil(items.length / COLS));
  const height = PAD * 2 + rows * CELL_H + (rows - 1) * GAP;
  const thumbs: string[] = [];
  for (const it of items) {
    const p = await store.prepare(it.image);
    const buf = await sharp(p.file).resize(CELL_W, THUMB_H, { fit: "contain", background: bg }).jpeg({ quality: 85 }).toBuffer();
    thumbs.push(`data:image/jpeg;base64,${buf.toString("base64")}`);
  }
  const sheet = {
    type: "div",
    props: {
      style: {
        boxSizing: "border-box",
        display: "flex",
        flexWrap: "wrap",
        alignContent: "flex-start",
        width: WIDTH,
        height,
        padding: PAD,
        gap: GAP,
        backgroundColor: bg,
      },
      children: items.map((it, i) => ({
        type: "div",
        props: {
          style: { display: "flex", flexDirection: "column", width: CELL_W, height: CELL_H },
          children: [
            { type: "img", props: { src: thumbs[i], width: CELL_W, height: THUMB_H } },
            {
              type: "div",
              props: {
                style: { display: "flex", color: fg, fontFamily: font.name, fontSize: LABEL, marginTop: 6 },
                children: it.label,
              },
            },
          ],
        },
      })),
    },
  };
  const svg = await satori(sheet as never, { width: WIDTH, height, fonts: [font] });
  return Buffer.from(new Resvg(svg, { font: { loadSystemFonts: false } }).render().asPng());
}
