import { useRef } from 'react';

// A PDF shown INSIDE the device page, with a Print button, instead of
// opened in a new tab. The trivia laptop runs the shell in the browser's
// kiosk mode: no tabs, no address bar, no back. A target="_blank" there is
// a tab nobody can leave, and the viewer's full-screen button is a second
// trap inside the first. Inline, the Home button stays on screen and
// printing is one tap. The same works on the iPad, which gets AirPrint
// from the print dialog.
export function PdfPane({ src, title }: { src: string; title: string }) {
  const frame = useRef<HTMLIFrameElement>(null);
  const print = () => {
    const w = frame.current?.contentWindow;
    if (w) {
      w.focus();
      w.print();
    }
  };
  return (
    <section className="pdf-pane">
      <div className="pdf-pane-bar">
        <span className="pdf-pane-title">{title}</span>
        <button className="btn device-btn-sm" type="button" onClick={print}>
          Print
        </button>
      </div>
      <iframe ref={frame} className="pdf-pane-frame" src={src} title={title} />
    </section>
  );
}
