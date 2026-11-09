import { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import type { PDFPageProxy } from 'pdfjs-dist/legacy/build/pdf.mjs';

// A PDF shown INSIDE the device page, with a Print button, instead of
// opened in a new tab. The trivia laptop runs the shell in the browser's
// kiosk mode: no tabs, no address bar, no back. A target="_blank" there is
// a tab nobody can leave, and the viewer's full-screen button is a second
// trap inside the first. Inline, the Home button stays on screen and
// printing is one tap. The same works on the iPad, which gets AirPrint
// from the print dialog.
//
// Print does NOT go through the iframe's viewer. contentWindow.print() on a
// framed PDF is at the browser's mercy: Safari prints the whole host page,
// and Chrome's out-of-process viewer can hand back its HTML wrapper, so the
// card came out as a web page -- URL and date in the margins, and shrunk to
// fit inside them. Instead the PDF is drawn to images and printed from this
// page, with an @page rule sized to the PDF's own sheet and no margin: no
// margin leaves the browser nowhere to put a header, and an image the size
// of the page has nothing to shrink.
//
// Except in Safari, which is the bar iPad. Safari draws its own title, URL
// and page number on any web page it prints, whatever the page's margins,
// so the images would come out with footers anyway. Safari only prints a PDF
// clean when the PDF is the document, so there Print hands the PDF itself to
// the share sheet, whose Print is AirPrint. It used to open the PDF in a tab
// and point at Safari's Share button, but the iPad now runs the console as a
// home-screen app: no tabs, no toolbar, no back, so that "tab" filled the
// screen with nothing to tap. The share sheet is a popover over this page,
// so dismissing it lands back here either way.
//
// navigator.share needs the tap's user activation, which an await on a fetch
// can spend, so the PDF is fetched when the pane opens and Print shares a
// file already in hand.

type Sheets = { urls: string[]; widthPt: number; heightPt: number };

// 200 dpi: sharp on a laser printer, and a Letter page stays well under
// the iPad's canvas size limit.
const PRINT_SCALE = 200 / 72;

export function PdfPane({ src, title }: { src: string; title: string }) {
  const [sheets, setSheets] = useState<Sheets | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [hint, setHint] = useState<string | null>(null);
  const printWhenMounted = useRef(false);
  const shareFile = useSharedPdf(src, title);

  useEffect(() => {
    if (!sheets || !printWhenMounted.current) return;
    printWhenMounted.current = false;
    // One frame for the portal's <style> and images to be in the document.
    requestAnimationFrame(() => window.print());
  }, [sheets]);

  // A new PDF (the other week, a reloaded menu) makes the old images stale.
  useEffect(() => () => sheets?.urls.forEach((u) => URL.revokeObjectURL(u)), [sheets]);
  useEffect(() => {
    setSheets(null);
    setHint(null);
  }, [src]);

  const print = async () => {
    setError(null);
    if (shareFile) {
      try {
        // No await before this: it must run inside the tap.
        await navigator.share({ files: [shareFile], title });
      } catch (e) {
        // Closing the sheet without printing is not an error.
        if (!(e instanceof DOMException && e.name === 'AbortError')) {
          setError(e instanceof Error ? e.message : String(e));
        }
      }
      return;
    }
    if (isSafari() && !isStandalone()) {
      // A browser tab has a toolbar and a way back; a home-screen app does
      // not, so there this falls through to printing images, footers and all,
      // rather than trapping anyone. Synchronous, inside the tap, or the popup
      // blocker eats it.
      window.open(src, '_blank');
      setHint('The PDF opened in a new tab. Tap the Share button there, then Print. Close that tab to come back.');
      return;
    }
    setBusy(true);
    try {
      const next = await rasterize(src);
      printWhenMounted.current = true;
      setSheets(next);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="pdf-pane">
      <div className="pdf-pane-bar">
        <span className="pdf-pane-title">{title}</span>
        <button
          className="btn device-btn-sm"
          type="button"
          onClick={() => void print()}
          disabled={busy || shareFile === undefined}
        >
          {busy || shareFile === undefined ? 'Preparing…' : 'Print'}
        </button>
      </div>
      {hint && <p className="banner banner-ok">{hint}</p>}
      {error && <p className="banner banner-error">Couldn’t prepare the print: {error}</p>}
      <iframe className="pdf-pane-frame" src={src} title={title} />
      {sheets && createPortal(<PrintSheets sheets={sheets} />, document.body)}
    </section>
  );
}

// isSafari reports a WebKit browser that adds its own print header and
// footer: Safari on a Mac, and every browser on an iPad or iPhone, which are
// all WebKit underneath. iPadOS reports itself as a Mac, so a touch screen
// is what gives it away.
function isSafari(): boolean {
  const ua = navigator.userAgent;
  if (/iPad|iPhone|iPod/.test(ua)) return true;
  if (/Macintosh/.test(ua) && navigator.maxTouchPoints > 1) return true;
  return /Safari\//.test(ua) && !/Chrome|Chromium|Edg\//.test(ua);
}

// isStandalone reports a home-screen app: no tabs, no address bar, no back.
function isStandalone(): boolean {
  const nav = navigator as Navigator & { standalone?: boolean };
  return nav.standalone === true || window.matchMedia('(display-mode: standalone)').matches;
}

// useSharedPdf fetches the PDF as a File ready for the share sheet, on the
// WebKit browsers that print through it. undefined while it loads (Print
// waits), null where sharing is not the way to print or the fetch failed --
// the caller then falls back rather than leaving Print dead.
function useSharedPdf(src: string, title: string): File | null | undefined {
  const wanted = isSafari() && typeof navigator.canShare === 'function';
  const [file, setFile] = useState<File | null | undefined>(wanted ? undefined : null);
  useEffect(() => {
    if (!wanted) return;
    let live = true;
    setFile(undefined);
    fetch(src, { credentials: 'same-origin' })
      .then((res) => {
        if (!res.ok) throw new Error(`the PDF did not load (${res.status})`);
        return res.blob();
      })
      .then((blob) => {
        const f = new File([blob], `${title}.pdf`, { type: 'application/pdf' });
        if (live) setFile(navigator.canShare({ files: [f] }) ? f : null);
      })
      .catch(() => {
        if (live) setFile(null);
      });
    return () => {
      live = false;
    };
  }, [src, title, wanted]);
  return file;
}

// PrintSheets is invisible on screen and is the only thing on paper while it
// is mounted. It sits directly under <body> so the rule hiding everything
// else can be a sibling selector.
function PrintSheets({ sheets }: { sheets: Sheets }) {
  const { widthPt: w, heightPt: h } = sheets;
  const css = `
@page { size: ${w}pt ${h}pt; margin: 0; }
@media screen { .print-sheets { display: none; } }
@media print {
  html, body { margin: 0 !important; padding: 0 !important; background: #fff !important; }
  body > :not(.print-sheets) { display: none !important; }
  .print-sheet { display: block; width: ${w}pt; height: ${h}pt; break-after: page; page-break-after: always; }
  .print-sheet:last-child { break-after: auto; page-break-after: auto; }
}`;
  return (
    <div className="print-sheets">
      <style>{css}</style>
      {sheets.urls.map((u, i) => (
        <img key={u} className="print-sheet" src={u} alt={`Page ${i + 1}`} />
      ))}
    </div>
  );
}

// rasterize draws every page of the PDF to a PNG. pdf.js is loaded on the
// first Print, not with the console: most pages never need it. The legacy
// build is the one that runs on an older iPad's Safari.
async function rasterize(src: string): Promise<Sheets> {
  const pdfjs = await import('pdfjs-dist/legacy/build/pdf.mjs');
  const worker = await import('pdfjs-dist/legacy/build/pdf.worker.min.mjs?url');
  pdfjs.GlobalWorkerOptions.workerSrc = worker.default;

  const res = await fetch(src, { credentials: 'same-origin' });
  if (!res.ok) throw new Error(`the PDF did not load (${res.status})`);
  const doc = await pdfjs.getDocument({ data: new Uint8Array(await res.arrayBuffer()) }).promise;
  try {
    const urls: string[] = [];
    let widthPt = 0;
    let heightPt = 0;
    for (let n = 1; n <= doc.numPages; n++) {
      const page = await doc.getPage(n);
      if (n === 1) {
        const v = page.getViewport({ scale: 1 });
        widthPt = v.width;
        heightPt = v.height;
      }
      urls.push(await pageToPNG(page));
    }
    await Promise.all(urls.map(decoded));
    return { urls, widthPt, heightPt };
  } finally {
    void doc.destroy();
  }
}

async function pageToPNG(page: PDFPageProxy): Promise<string> {
  const viewport = page.getViewport({ scale: PRINT_SCALE });
  const canvas = document.createElement('canvas');
  canvas.width = Math.ceil(viewport.width);
  canvas.height = Math.ceil(viewport.height);
  const ctx = canvas.getContext('2d');
  if (!ctx) throw new Error('this browser cannot draw the page');
  ctx.fillStyle = '#fff';
  ctx.fillRect(0, 0, canvas.width, canvas.height);
  await page.render({ canvasContext: ctx, viewport }).promise;
  const blob = await new Promise<Blob | null>((ok) => canvas.toBlob(ok, 'image/png'));
  if (!blob) throw new Error('the page could not be turned into an image');
  return URL.createObjectURL(blob);
}

// decoded resolves once an image is ready to paint, so the print dialog
// never snapshots a blank sheet.
function decoded(url: string): Promise<void> {
  const img = new Image();
  img.src = url;
  return img.decode();
}
