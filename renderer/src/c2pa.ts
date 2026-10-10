// A cheap provenance check. Full C2PA verification needs a signature chain;
// what a poster needs to know is only whether the file says it was made by a
// model. Two places say so: a C2PA manifest (JUMBF box) whose actions carry
// the trainedAlgorithmicMedia source type, and IPTC's DigitalSourceType in
// XMP, which most generators write even without a manifest.

export type Verdict = "none" | "camera" | "ai" | "unknown";

const AI_MARKERS = [
  "trainedalgorithmicmedia",
  "compositewithtrainedalgorithmicmedia",
  "c2pa.created\u0000\u0000digitalsourcetype",
];

const CAMERA_MARKERS = ["digitalcapture", "c2pa.created", "c2pa.actions"];

export function c2paVerdict(bytes: Buffer): Verdict {
  // Search only the headers: manifests and XMP sit before the image data,
  // and a 40MB scan per photo is the wrong price for a flag.
  const head = bytes.subarray(0, Math.min(bytes.length, 4 << 20)).toString("latin1").toLowerCase();
  const hasManifest = head.includes("jumb") && head.includes("c2pa");
  const hasAIXmp = head.includes("digitalsourcetype") && AI_MARKERS.some((m) => head.includes(m));
  if (hasAIXmp) return "ai";
  if (!hasManifest) return "none";
  if (AI_MARKERS.some((m) => head.includes(m))) return "ai";
  if (CAMERA_MARKERS.some((m) => head.includes(m))) return "camera";
  return "unknown";
}
