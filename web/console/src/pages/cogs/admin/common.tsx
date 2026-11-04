import { Link } from 'react-router-dom';
import type { BrewVessel, VesselKind } from '../../../api';

// Shared bits of the brewing console pages: the crumbs, number parsing and
// the labels the pages show for codes.

export function Crumbs({ page }: { page: string }) {
  return (
    <nav className="crumbs">
      <Link to="/">Home</Link>
      <span className="crumb-sep">/</span>
      <Link to="/cogs">COGS</Link>
      <span className="crumb-sep">/</span>
      <span>{page}</span>
    </nav>
  );
}

// num reads a form field as a number, 0 when blank or not a number.
export function num(v: string): number {
  const n = parseFloat(v);
  return Number.isFinite(n) ? n : 0;
}

export function today(): string {
  const d = new Date();
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

export const KIND_LABELS: Record<VesselKind, string> = {
  fermenter: 'Fermenter',
  brite: 'Brite tank',
  serving: 'Serving tank',
  barrel: 'Barrel',
};

export const STAGES: { code: string; label: string }[] = [
  { code: 'fermenting', label: 'Fermenting' },
  { code: 'dry_hopped', label: 'Dry hopped' },
  { code: 'conditioning', label: 'Conditioning' },
  { code: 'brite', label: 'In the brite' },
  { code: 'waiting', label: 'Waiting to go on tap' },
  { code: 'pouring', label: 'Pouring' },
  { code: 'aging', label: 'Aging' },
];

export function defaultStage(kind: VesselKind): string {
  switch (kind) {
    case 'brite':
      return 'brite';
    case 'serving':
      return 'waiting';
    case 'barrel':
      return 'aging';
    default:
      return 'fermenting';
  }
}

export function vesselName(vessels: { id: string; name: string }[], id: string): string {
  return vessels.find((v) => v.id === id)?.name ?? 'a vessel';
}

export function isEmptyVessel(v: BrewVessel): boolean {
  return v.batch_id === null && v.volume_bbl < 0.005;
}

export function fmtDate(iso: string): string {
  const d = new Date(iso.length === 10 ? `${iso}T12:00:00` : iso);
  return d.toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric' });
}
