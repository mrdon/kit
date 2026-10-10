import { Link, useLocation } from 'react-router-dom';
import { PosterGenerateStage, PosterOption, PosterStatus } from '../../api';

// Pieces the Posters pages share: the sub-tab row, the option grid that both
// the event drawer and the poster page show, and the small formatters.

const TABS = [
  { to: '/posters', label: 'Posters' },
  { to: '/posters/templates', label: 'Templates' },
  { to: '/posters/photos', label: 'Photos' },
];

// PostersTabs is the chip row under the page head on the three list pages.
// Exact match on the path: /posters/templates must not light up Posters.
export function PostersTabs() {
  const { pathname } = useLocation();
  return (
    <nav className="filter-chips posters-tabs" aria-label="Posters sections">
      {TABS.map((t) => (
        <Link
          key={t.to}
          to={t.to}
          className={`chip${pathname === t.to ? ' chip-active' : ''}`}
        >
          {t.label}
        </Link>
      ))}
    </nav>
  );
}

// stageText is what the Generate button says while the server works.
export function stageText(stage: PosterGenerateStage | null): string {
  switch (stage) {
    case PosterGenerateStage.Copy:
      return 'Writing the copy…';
    case PosterGenerateStage.Photo:
      return 'Choosing a photo…';
    case PosterGenerateStage.Rendering:
      return 'Rendering options…';
    default:
      return 'Starting…';
  }
}

// OptionGrid shows a batch of portrait renders. The one on the event is
// marked, and so is the current one when the caller tracks it; clicking any
// other hands it back to the caller, who decides whether that means
// "publish" (the drawer) or "select" (the poster page).
export function OptionGrid({
  options,
  setId,
  currentId,
  busy,
  onPick,
  pickLabel = 'Set on event',
}: {
  options: PosterOption[];
  setId?: string;
  currentId?: string;
  busy: boolean;
  onPick: (o: PosterOption) => void;
  pickLabel?: string;
}) {
  if (options.length === 0) return null;
  return (
    <div className="poster-options">
      {options.map((o) => {
        const on = o.id === setId;
        const current = !on && o.id === currentId;
        return (
          <button
            key={o.id}
            type="button"
            className={`poster-option${on ? ' poster-option-on' : ''}${current ? ' poster-option-current' : ''}`}
            disabled={busy || on || current}
            onClick={() => onPick(o)}
            title={on ? 'On the event' : current ? 'Current' : pickLabel}
          >
            <img src={o.thumb} alt={o.content?.title || 'Poster option'} loading="lazy" />
            {on && <span className="badge poster-option-badge">On the event</span>}
            {current && <span className="badge poster-option-badge">Current</span>}
          </button>
        );
      })}
    </div>
  );
}

// statusPill maps a poster's status to the console's pill colours.
export function statusPill(status: PosterStatus): string {
  switch (status) {
    case 'on the event':
      return 'pill pill-ok';
    case 'out of date':
      return 'pill pill-error';
    default:
      return 'pill pill-off';
  }
}

// fmtWhen is a short, local timestamp for lists: "Oct 3, 14:05".
export function fmtWhen(iso?: string | null): string {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString(undefined, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

// fmtDay is a date without a time, for "last used" and "derived on".
export function fmtDay(iso?: string | null): string {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
}

// errText reads a thrown value as a sentence for a banner.
export const errText = (e: unknown) => (e instanceof Error ? e.message : String(e));
