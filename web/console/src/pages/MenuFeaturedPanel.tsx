import { useEffect, useState } from 'react';
import {
  api,
  type MenuFeatured,
  type MenuFeaturedPhoto,
  type MenuPrintBeer,
} from '../api';

// The featured beer on the printed menu: one beer boxed at the head of the
// first column, starred in its own section.
//
// The photo is found by name, with no model involved: the wall's spotlight
// image for that beer, a menu image named beer-<name>, then library photos
// tagged with the beer. "Auto" takes the first; any of them can be clicked
// instead, or none. The server keeps the chosen photo when this is saved, so
// printing never waits on Drive.

const DEFAULT_LABEL = "Brewer's pick";

interface Props {
  featured: MenuFeatured | undefined;
  beers: MenuPrintBeer[];
  onChange: (next: MenuFeatured | undefined) => void;
}

export default function MenuFeaturedPanel({ featured, beers, onChange }: Props) {
  const beer = featured?.beer ?? '';
  const photo = featured?.photo || 'auto';
  const [candidates, setCandidates] = useState<MenuFeaturedPhoto[]>([]);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!beer) {
      setCandidates([]);
      return;
    }
    let live = true;
    setLoading(true);
    api
      .menuFeaturedPhotos(beer)
      .then((r) => live && setCandidates(r.candidates))
      .catch(() => live && setCandidates([]))
      .finally(() => live && setLoading(false));
    return () => {
      live = false;
    };
  }, [beer]);

  function patch(p: Partial<MenuFeatured>) {
    onChange({ beer, ...featured, ...p });
  }

  function pickBeer(name: string) {
    // A new beer starts from its own photo, not the last one's.
    onChange(name ? { ...featured, beer: name, photo: 'auto' } : undefined);
  }

  const note = beers.find((b) => b.name === beer)?.note ?? '';
  const onTap = !beer || beers.some((b) => b.name === beer);
  // A saved photo is stored as a menu image, which may not be one of the
  // candidates any more (a library photo is kept under its own key).
  const current =
    photo.startsWith('asset:') && !candidates.some((c) => c.ref === photo)
      ? [{ ref: photo, label: 'Current photo' }]
      : [];

  return (
    <section className="panel">
      <h2 className="panel-title">Featured beer</h2>
      <p className="card-desc">
        One beer boxed at the top of the menu, with a star beside it in its own
        section. It only prints while that beer is on tap.
      </p>
      <label className="field">
        <span>Beer</span>
        <select value={beer} onChange={(e) => pickBeer(e.target.value)}>
          <option value="">None</option>
          {!onTap && <option value={beer}>{beer} (not on tap)</option>}
          {beers.map((b) => (
            <option key={b.name} value={b.name}>
              {b.name}
            </option>
          ))}
        </select>
      </label>
      {beer && (
        <>
          <div className="field-row">
            <label className="field">
              <span>Why it's featured</span>
              <input
                value={featured?.label ?? ''}
                placeholder={DEFAULT_LABEL}
                maxLength={30}
                onChange={(e) => patch({ label: e.target.value })}
              />
              <span className="field-note">Last keg, Seasonal, New release…</span>
            </label>
          </div>
          <label className="field">
            <span>Text in the box</span>
            <textarea
              rows={3}
              maxLength={240}
              value={featured?.text ?? ''}
              placeholder={note || 'Its description from the tap list'}
              onChange={(e) => patch({ text: e.target.value })}
            />
            <span className="field-note">Leave empty to use its description.</span>
          </label>
          <PhotoPicker
            photo={photo}
            options={[...current, ...candidates]}
            loading={loading}
            onPick={(ref) => patch({ photo: ref })}
          />
        </>
      )}
    </section>
  );
}

interface PickerProps {
  photo: string;
  options: MenuFeaturedPhoto[];
  loading: boolean;
  onPick: (ref: string) => void;
}

function PhotoPicker({ photo, options, loading, onPick }: PickerProps) {
  const tile = (selected: boolean) => ({
    width: 72,
    height: 108,
    objectFit: 'cover' as const,
    borderRadius: 4,
    cursor: 'pointer',
    outline: selected ? '3px solid #d9a03c' : '1px solid rgba(128,128,128,.4)',
    outlineOffset: 2,
  });
  return (
    <div className="field">
      <span>Photo</span>
      <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', alignItems: 'center' }}>
        <button
          type="button"
          className={photo === 'auto' ? 'btn' : 'btn btn-ghost'}
          onClick={() => onPick('auto')}
        >
          Auto
        </button>
        <button
          type="button"
          className={photo === 'none' ? 'btn' : 'btn btn-ghost'}
          onClick={() => onPick('none')}
        >
          No photo
        </button>
        {options.map((o) => (
          <img
            key={o.ref}
            src={api.menuFeaturedPhotoUrl(o.ref)}
            alt={o.label}
            title={o.label}
            style={tile(photo === o.ref)}
            onClick={() => onPick(o.ref)}
          />
        ))}
      </div>
      <span className="field-note">
        {loading
          ? 'Looking for photos…'
          : options.length === 0
            ? 'No photo of this beer found — the box prints without one.'
            : 'Auto uses the first. Photos come from the wall’s spotlight, menu images named for the beer, and library photos tagged with it.'}
      </span>
    </div>
  );
}
