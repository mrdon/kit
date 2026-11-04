import { useEffect, useState, type ReactNode } from 'react';
import type { ActionInput, BrewFormat, LineInput } from '../../api';
import { useBrew } from './context';

// The brewer's controls: steppers sized for a thumb, a 7-day date row, the
// sheet every form sits in. Counts that are ground truth (kegs, cans) start
// at 0 and are stepped up; nothing here pre-fills them.

export function Sheet({
  title,
  sub,
  onClose,
  children,
}: {
  title: string;
  sub?: ReactNode;
  onClose: () => void;
  children: ReactNode;
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);
  return (
    <div
      className="brew-sheet-backdrop"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <section className="brew-sheet" role="dialog" aria-modal="true" aria-label={title}>
        <div className="brew-sheet-head">
          <div>
            <h2>{title}</h2>
            {sub && <p className="brew-sheet-sub">{sub}</p>}
          </div>
          <button type="button" className="brew-btn" onClick={onClose}>
            Close
          </button>
        </div>
        {children}
      </section>
    </div>
  );
}

export function Field({ label, children, hint }: { label: string; children: ReactNode; hint?: ReactNode }) {
  return (
    <div className="brew-section">
      <span className="brew-label">{label}</span>
      {children}
      {hint && <p className="brew-hint">{hint}</p>}
    </div>
  );
}

export function Stepper({
  value,
  onChange,
  step = 1,
  big,
  min = 0,
  max,
  unit,
  label,
  decimals = 0,
}: {
  value: number;
  onChange: (v: number) => void;
  step?: number;
  big?: number;
  min?: number;
  max?: number;
  unit?: string;
  label: string;
  decimals?: number;
}) {
  // While the field is being typed in, its text is kept as typed: "7." has
  // to survive long enough to become "7.5".
  const [draft, setDraft] = useState<string | null>(null);
  const clamp = (v: number) => {
    let out = Math.max(min, v);
    if (max !== undefined) out = Math.min(max, out);
    return Number(out.toFixed(3));
  };
  const shown = Number.isFinite(value) ? String(Number(value.toFixed(Math.max(decimals, 2)))) : '';
  return (
    <span className="brew-stepper">
      {big !== undefined && (
        <button type="button" aria-label={`${label} minus ${big}`} onClick={() => onChange(clamp(value - big))}>
          −{big}
        </button>
      )}
      <button type="button" aria-label={`${label} minus ${step}`} onClick={() => onChange(clamp(value - step))}>
        −
      </button>
      <input
        aria-label={label}
        inputMode="decimal"
        value={draft ?? shown}
        onFocus={() => setDraft(shown)}
        onBlur={() => setDraft(null)}
        onChange={(e) => {
          setDraft(e.target.value);
          const v = parseFloat(e.target.value);
          onChange(Number.isFinite(v) ? clamp(v) : 0);
        }}
      />
      <button type="button" aria-label={`${label} plus ${step}`} onClick={() => onChange(clamp(value + step))}>
        +
      </button>
      {big !== undefined && (
        <button type="button" aria-label={`${label} plus ${big}`} onClick={() => onChange(clamp(value + big))}>
          +{big}
        </button>
      )}
      {unit && <span className="brew-stepper-unit">{unit}</span>}
    </span>
  );
}

// whenOptions are Today, Yesterday, then back through the last 7 days.
// Today is sent as '' (now); any other day the server stamps at noon.
export function whenOptions(today: string): { value: string; label: string }[] {
  const base = new Date(`${today}T12:00:00`);
  const out = [{ value: '', label: 'Today' }];
  for (let i = 1; i <= 7; i++) {
    const d = new Date(base);
    d.setDate(base.getDate() - i);
    const iso = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
    out.push({
      value: iso,
      label: i === 1 ? 'Yesterday' : `${d.toLocaleDateString(undefined, { weekday: 'short' })} ${d.getDate()}`,
    });
  }
  return out;
}

export function WhenChips({ today, value, onChange }: { today: string; value: string; onChange: (v: string) => void }) {
  // The iPad goes back a week; an admin correcting an older entry can pick
  // any past date (the server enforces the same split).
  const { isAdmin } = useBrew();
  const options = whenOptions(today);
  const older = value !== '' && !options.some((o) => o.value === value);
  return (
    <Field label="When">
      <div className="brew-chips">
        {options.map((o) => (
          <button
            key={o.value || 'today'}
            type="button"
            className={`brew-chip ${value === o.value ? 'brew-chip-on' : ''}`}
            onClick={() => onChange(o.value)}
          >
            {o.label}
          </button>
        ))}
        {isAdmin && (
          <input
            type="date"
            aria-label="An older date"
            className={`brew-chip ${older ? 'brew-chip-on' : ''}`}
            max={today}
            value={older ? value : ''}
            onChange={(e) => onChange(e.target.value === today ? '' : e.target.value)}
          />
        )}
      </div>
    </Field>
  );
}

export function Chip({
  on,
  onClick,
  children,
  sub,
  disabled,
}: {
  on: boolean;
  onClick: () => void;
  children: ReactNode;
  sub?: ReactNode;
  disabled?: boolean;
}) {
  return (
    <button type="button" className={`brew-chip ${on ? 'brew-chip-on' : ''}`} onClick={onClick} disabled={disabled}>
      {children}
      {sub && <small>{sub}</small>}
    </button>
  );
}

export function Check({ checked, onChange, children }: { checked: boolean; onChange: (v: boolean) => void; children: ReactNode }) {
  return (
    <label className="brew-check">
      <input type="checkbox" checked={checked} onChange={(e) => onChange(e.target.checked)} />
      {children}
    </label>
  );
}

// A packaged count for one format: kegs as a number, cans and bottles as
// cases plus loose units.
export interface Qty {
  full: number;
  partial: number;
  cases: number;
  loose: number;
}

export const zeroQty: Qty = { full: 0, partial: 0, cases: 0, loose: 0 };

export function qtyUnits(q: Qty, f: BrewFormat, partialFullness: number): number {
  if (f.kind === 'keg') return q.full + q.partial * partialFullness;
  return q.cases * f.units_per_case + q.loose;
}

export function qtyLine(q: Qty, f: BrewFormat, extra: Partial<LineInput> = {}): LineInput {
  if (f.kind === 'keg') return { format_id: f.id, full: q.full, partial: q.partial, ...extra };
  return { format_id: f.id, cases: q.cases, loose: q.loose, ...extra };
}

export function QtyInput({
  format,
  value,
  onChange,
  partial,
}: {
  format: BrewFormat;
  value: Qty;
  onChange: (q: Qty) => void;
  partial?: boolean;
}) {
  if (format.kind === 'keg') {
    return (
      <span className="brew-chips">
        <Stepper label={`${format.name} full`} value={value.full} big={5} onChange={(full) => onChange({ ...value, full })} unit={partial ? 'full' : undefined} />
        {partial && (
          <Stepper label={`${format.name} partial`} value={value.partial} onChange={(p) => onChange({ ...value, partial: p })} unit="partial" />
        )}
      </span>
    );
  }
  return (
    <span className="brew-chips">
      <Stepper label={`${format.name} cases`} value={value.cases} big={5} onChange={(cases) => onChange({ ...value, cases })} unit="cases" />
      <Stepper label={`${format.name} loose`} value={value.loose} big={6} onChange={(loose) => onChange({ ...value, loose })} unit="loose" />
    </span>
  );
}

export function SaveBar({
  onCancel,
  onSave,
  saving,
  disabled,
  label = 'Save',
  error,
}: {
  onCancel: () => void;
  onSave: () => void;
  saving: boolean;
  disabled?: boolean;
  label?: string;
  error?: string | null;
}) {
  return (
    <>
      {error && <p className="brew-error">{error}</p>}
      <div className="brew-sheet-foot">
        <button type="button" className="brew-btn brew-btn-big" onClick={onCancel}>
          Cancel
        </button>
        <button
          type="button"
          className="brew-btn brew-btn-primary brew-btn-big"
          onClick={onSave}
          disabled={saving || disabled}
        >
          {saving ? 'Saving…' : label}
        </button>
      </div>
    </>
  );
}

// useSave runs one action from a form: busy while it's in flight, the
// server's refusal kept for the form to show.
export function useSave() {
  const { record } = useBrew();
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const save = async (input: ActionInput) => {
    setSaving(true);
    setError(null);
    const err = await record(input);
    setSaving(false);
    if (err) setError(err);
  };
  return { saving, error, setError, save };
}
