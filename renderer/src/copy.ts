// Copy rules a machine can check: the mechanical tells from the writing-copy
// skill and the brand guide. The read-aloud test is still a person's job.
import type { Content, CopyCheck } from "./types.ts";

export type { CopyCheck };

// Filler and sales words no venue's voice wants. Tenant-specific terms (a
// brewery's "hoppy hour") come from the brand's bannedWords.
export const BANNED_WORDS = [
  "elevate", "curated", "vibrant", "nestled", "immerse", "dive in", "unleash", "boasts",
  "seamless", "unlock", "bespoke", "meticulous", "intricate", "underscore", "testament",
  "tapestry", "realm", "landscape", "amazing", "unforgettable", "don't miss", "dont miss",
  "thrilled to announce", "tag a friend", "limited time", "don't miss out", "only a few left",
  "look no further", "we've got you covered",
];

const BANNED_PATTERNS: Array<[RegExp, string]> = [
  [/[—–]/, "em or en dash; use a period, comma, colon, or 'to'"],
  [/!/, "exclamation mark"],
  [/\d\s?-\s?\d/, "hyphenated range; write '3 to 10pm'"],
  [/\$\d+\.00\b/, "empty decimals in a price; write $5"],
  [/\bwhether you'?re\b/i, "'Whether you're X or Y'"],
  [/\bit'?s not just\b|\bthis isn'?t\b/i, "'It's not just X, it's Y'"],
  [/\b[A-Z]{3,}\b/, "ALL CAPS word"],
  [/\p{Extended_Pictographic}/u, "emoji in a graphic"],
];

// Checks one labelled piece of text.
export function checkText(field: string, text: string, extraBanned: string[] = []): CopyCheck {
  const problems: string[] = [];
  const warnings: string[] = [];
  const lower = text.toLowerCase();
  for (const w of [...BANNED_WORDS, ...extraBanned]) {
    if (w && lower.includes(w.toLowerCase())) problems.push(`${field}: banned "${w}"`);
  }
  for (const [re, why] of BANNED_PATTERNS) if (re.test(text)) problems.push(`${field}: ${why}`);
  for (const s of text.split(/[.:]\s+/)) {
    if (s.split(/\s+/).length > 25) warnings.push(`${field}: a sentence over 25 words`);
  }
  return { problems, warnings };
}

// Checks a whole content object the way the prototype's check.mjs did.
export function checkContent(content: Content, extraBanned: string[] = []): CopyCheck {
  const problems: string[] = [];
  const warnings: string[] = [];
  const pieces: Array<[string, string | undefined]> = [
    ["eyebrow", content.eyebrow],
    ["title", content.title],
    ["summary", content.summary],
    ["action", content.action],
    ...(content.details ?? []).flatMap((d, i): Array<[string, string]> => [
      [`details[${i}].label`, d.label],
      [`details[${i}].value`, d.value],
    ]),
  ];
  for (const [field, text] of pieces) {
    if (!text) continue;
    const r = checkText(field, text, extraBanned);
    problems.push(...r.problems);
    warnings.push(...r.warnings);
  }
  if (!content.eyebrow?.trim()) problems.push("eyebrow: required");
  if (!content.title?.trim()) problems.push("title: required");
  if (content.eyebrow && content.eyebrow.length > 28) problems.push("eyebrow: over 28 characters");
  if (content.title && content.title.length > 60) problems.push("title: over 60 characters");
  if (content.summary && content.summary.length > 140) problems.push("summary: over 140 characters");
  if (content.action && content.action.length > 70) problems.push("action: over 70 characters");
  if ((content.details ?? []).length > 3) problems.push("details: at most 3");
  for (const [i, d] of (content.details ?? []).entries()) {
    if (d.label.length > 14) problems.push(`details[${i}].label: over 14 characters`);
    if (d.value.length > 48) problems.push(`details[${i}].value: over 48 characters`);
  }
  if (content.title && /\?\s*$/.test(content.title)) problems.push("title: rhetorical question as the opener");
  const later = (content.title ?? "").split(/\s+/).slice(1).filter((w) => w.length > 3);
  if (later.length >= 2 && later.filter((w) => /^[A-Z]/.test(w)).length > later.length / 2) {
    warnings.push("title looks like Title Case; fine for an event's proper name, otherwise use sentence case");
  }
  return { problems, warnings };
}
