import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  api,
  type PosterBrandStatus,
  type PosterLogoCandidate,
  type PosterLogoFile,
  type PosterSyncResult,
  type PostersSettings as Payload,
} from '../../api';
import { useSetChatContext } from '../../chatContext';
import { errText, fmtDay } from './common';

// Admin setup for posters: the two Drive folders, which logo file is which
// variant, the stock-photo switch, the photo index status, and the brand
// Kit derived from the branding-guide skill, read-only. There is no brand
// form: to change the brand, edit the skill.

export default function PostersSettings() {
  useSetChatContext('the admin Posters settings page');
  const [st, setSt] = useState<Payload | null>(null);
  const [photoFolder, setPhotoFolder] = useState('');
  const [logoFolder, setLogoFolder] = useState('');
  const [logoFiles, setLogoFiles] = useState<PosterLogoCandidate[]>([]);
  const [allowStock, setAllowStock] = useState(false);
  const [autoSync, setAutoSync] = useState(false);
  const [sync, setSync] = useState<PosterSyncResult | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [copied, setCopied] = useState(false);

  const apply = (next: Payload) => {
    setSt(next);
    setPhotoFolder(next.photo_folder_url);
    setLogoFolder(next.logo_folder_url);
    setLogoFiles(next.logo_files ?? []);
    setAllowStock(next.settings.allow_stock_photos);
    setAutoSync(next.settings.auto_sync);
  };

  useEffect(() => {
    api.postersSettings().then(apply).catch((e) => setErr(errText(e)));
  }, []);

  const run = async (fn: () => Promise<void>) => {
    setBusy(true);
    setErr(null);
    setNote(null);
    try {
      await fn();
    } catch (e) {
      setErr(errText(e));
    } finally {
      setBusy(false);
    }
  };

  // One file per variant: choosing a variant that another file holds moves
  // it, since two files for "white" would leave the renderer guessing.
  const setVariant = (fileId: string, variant: string) =>
    setLogoFiles((fs) =>
      fs.map((f) => {
        if (f.file_id === fileId) return { ...f, variant: variant || undefined };
        if (variant && f.variant === variant) return { ...f, variant: undefined };
        return f;
      }),
    );

  const logoMap = (): Record<string, PosterLogoFile> => {
    const m: Record<string, PosterLogoFile> = {};
    for (const f of logoFiles) {
      if (f.variant) m[f.variant] = { file_id: f.file_id, name: f.name, modified: f.modified };
    }
    return m;
  };

  const save = (e: React.FormEvent) => {
    e.preventDefault();
    return run(async () => {
      const r = await api.savePostersSettings({
        photo_folder_url: photoFolder.trim(),
        logo_folder_url: logoFolder.trim(),
        logo_map: logoMap(),
        allow_stock_photos: allowStock,
        auto_sync: autoSync,
      });
      apply(r);
      setNote('Saved.');
    });
  };

  const syncNow = () =>
    run(async () => {
      const r = await api.postersSyncNow();
      setSync(r.result);
      apply(await api.postersSettings());
    });

  const rederive = () =>
    run(async () => {
      apply(await api.postersRederive());
      setNote('Brand re-derived from the branding guide.');
    });

  const copyPrompt = async () => {
    if (!st) return;
    try {
      await navigator.clipboard.writeText(st.index_prompt);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* the box is selectable; copying by hand still works */
    }
  };

  const counts = st?.counts;

  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <Link to="/admin">Admin</Link>
          <span className="crumb-sep">/</span>
          <span>Posters settings</span>
        </nav>
        <h1>Posters settings</h1>
        <p className="page-sub">
          Where the photos and logos live, and the brand Kit works from. Posters themselves
          are made from an event, or on the <Link to="/posters">Posters</Link> page.
        </p>
      </div>

      {note && (
        <p className="banner banner-ok" onClick={() => setNote(null)}>
          {note}
        </p>
      )}
      {err && <p className="banner banner-error">{err}</p>}
      {!st && !err && <p className="muted">Loading…</p>}

      {st && (
        <>
          <p className="status-line">
            Renderer:{' '}
            <span className={`pill ${st.renderer_ready ? 'pill-ok' : 'pill-error'}`}>
              {st.renderer_ready ? 'running' : 'not running'}
            </span>
          </p>

          <form onSubmit={save}>
            <section className="panel">
              <h2 className="panel-title">Google Drive folders</h2>
              <p className="status-line">
                Paste a link to each folder. They must be shared as "anyone with the link can
                view"; Kit lists them with its own key and never copies the files.
              </p>
              <label className="field">
                <span>Photo folder link</span>
                <input
                  value={photoFolder}
                  onChange={(e) => setPhotoFolder(e.target.value)}
                  placeholder="https://drive.google.com/drive/folders/…"
                />
                <span className="field-note">
                  Subfolders are sets: a poster borrows a second photo only from the hero's folder.
                </span>
              </label>
              <label className="field">
                <span>Logo folder link</span>
                <input
                  value={logoFolder}
                  onChange={(e) => setLogoFolder(e.target.value)}
                  placeholder="https://drive.google.com/drive/folders/…"
                />
              </label>
              {st.logo_error && <p className="field-hint">Logo folder: {st.logo_error}</p>}

              {logoFiles.length > 0 && (
                <>
                  <h3 className="poster-subhead">Logos</h3>
                  <p className="field-note">
                    Match each file to the variant the brand guide names. Files named after a
                    variant (gravity-white.png) map themselves on save.
                  </p>
                  {st.logo_variants.length === 0 && (
                    <p className="field-hint">The brand names no logo variants yet, so there is nothing to map.</p>
                  )}
                  <div className="poster-logo-grid">
                    {logoFiles.map((f) => (
                      <div key={f.file_id} className="poster-logo">
                        <img src={f.thumb} alt={f.name} referrerPolicy="no-referrer" loading="lazy" />
                        <div className="poster-logo-name" title={f.name}>
                          {f.name}
                        </div>
                        <select
                          value={f.variant ?? ''}
                          onChange={(e) => setVariant(f.file_id, e.target.value)}
                          aria-label={`Variant for ${f.name}`}
                        >
                          <option value="">not used</option>
                          {st.logo_variants.map((v) => (
                            <option key={v} value={v}>
                              {v}
                            </option>
                          ))}
                        </select>
                      </div>
                    ))}
                  </div>
                </>
              )}
            </section>

            <section className="panel">
              <h2 className="panel-title">Stock photos</h2>
              <label className="check">
                <input
                  type="checkbox"
                  checked={allowStock}
                  disabled={!st.stock_configured}
                  onChange={(e) => setAllowStock(e.target.checked)}
                />
                Let Kit use stock photos (Pixabay) when the library has nothing that fits
              </label>
              <p className="field-note">
                Stock images cannot be guaranteed camera-made. Off, Kit falls back to type-only
                layouts rather than a photo of something else.
              </p>
              {!st.stock_configured && (
                <p className="field-note">Not available: this server has no Pixabay key configured.</p>
              )}
            </section>

            <section className="panel">
              <h2 className="panel-title">Drive sync</h2>
              <label className="check">
                <input type="checkbox" checked={autoSync} onChange={(e) => setAutoSync(e.target.checked)} />
                List the photo folder every hour
              </label>
              <p className="field-note">
                Off by default. A harness indexing over MCP syncs for itself, and Sync photos now
                is always available below. Listing never calls a model; it only costs Drive requests.
              </p>
            </section>

            <div className="drawer-actions">
              <button type="submit" className="btn" disabled={busy}>
                {busy ? 'Saving…' : 'Save'}
              </button>
            </div>
          </form>

          <section className="panel">
            <h2 className="panel-title">Photo index</h2>
            {counts && (
              <div className="filter-chips">
                <span className="chip">
                  Pending <span className="chip-count">{counts.pending}</span>
                </span>
                <span className="chip">
                  Indexed <span className="chip-count">{counts.indexed}</span>
                </span>
                <span className="chip">
                  Removed <span className="chip-count">{counts.removed}</span>
                </span>
              </div>
            )}
            <p className="status-line">
              {st.settings.last_sync_at
                ? `Last synced ${fmtDay(st.settings.last_sync_at)}.`
                : 'Not synced yet. Press Sync photos now, or turn on the hourly sync above.'}
              {st.settings.last_sync_error && (
                <>
                  {' '}
                  <span className="error-text">{st.settings.last_sync_error}</span>
                </>
              )}
            </p>
            <div className="drawer-actions">
              <button type="button" className="btn btn-ghost" disabled={busy || !st.settings.photo_folder_id} onClick={syncNow}>
                Sync photos now
              </button>
              <Link to="/posters/photos" className="btn btn-ghost">
                Open the library
              </Link>
            </div>
            {sync && (
              <p className="status-line">
                Listed {sync.listed}, {sync.new} new, {sync.inspected} inspected, {sync.removed} removed,{' '}
                {sync.pending} pending.
                {sync.problems && sync.problems.length > 0 && (
                  <>
                    {' '}
                    <span className="error-text">{sync.problems.join('; ')}</span>
                  </>
                )}
              </p>
            )}
            {counts && counts.pending > 0 && (
              <>
                <p className="field-note">
                  {counts.pending} {counts.pending === 1 ? 'photo is' : 'photos are'} waiting for
                  descriptions. Kit does not spend its own model on this; paste the prompt into
                  Claude Code connected to this workspace's MCP.
                </p>
                <div className="snippet-box poster-prompt">
                  <pre className="snippet">{st.index_prompt}</pre>
                  <button type="button" className="btn btn-ghost btn-sm" onClick={copyPrompt}>
                    {copied ? 'Copied' : 'Copy'}
                  </button>
                </div>
              </>
            )}
          </section>

          <BrandPanel brand={st.brand} fontProblems={st.font_problems} busy={busy} onRederive={rederive} />
        </>
      )}
    </div>
  );
}

function Swatch({ name, color }: { name: string; color: string }) {
  return (
    <span className="poster-swatch" title={`${name}: ${color}`}>
      <span className="poster-swatch-chip" style={{ background: color }} />
      <span className="poster-swatch-name">{name}</span>
      <code>{color}</code>
    </span>
  );
}

function BrandPanel({
  brand,
  fontProblems,
  busy,
  onRederive,
}: {
  brand: PosterBrandStatus | null;
  fontProblems: string[];
  busy: boolean;
  onRederive: () => void;
}) {
  const b = brand?.brand;
  const tokens = b?.tokens ?? {};
  const grounds = b?.grounds ?? {};
  const accents = b?.accents ?? {};
  const formats = b?.formats ?? {};
  const problems = [...(brand?.problems ?? []), ...(fontProblems ?? [])];
  const sourceWords = brand?.source === 'json' ? 'the guide\'s token block' : brand?.source === 'model' ? 'the guide\'s prose' : null;

  return (
    <section className="panel">
      <h2 className="panel-title">Derived brand</h2>
      <p className="status-line">
        {sourceWords ? (
          <>
            Derived from the <strong>branding-guide</strong> skill ({sourceWords})
            {brand?.updated_at && <> on {fmtDay(brand.updated_at)}</>}. To change it, edit the skill.
          </>
        ) : (
          <>No branding-guide skill found yet. Add one and the brand derives itself.</>
        )}
      </p>
      {problems.length > 0 && (
        <ul className="poster-problems">
          {problems.map((p, i) => (
            <li key={i} className="error-text">
              {p}
            </li>
          ))}
        </ul>
      )}

      {b && (
        <>
          <h3 className="poster-subhead">Colours</h3>
          <div className="poster-swatches">
            {Object.entries(tokens).map(([name, color]) => (
              <Swatch key={name} name={name} color={color} />
            ))}
          </div>

          <h3 className="poster-subhead">Grounds</h3>
          <table className="item-table poster-table">
            <thead>
              <tr>
                <th>Ground</th>
                <th>Background</th>
                <th>Text</th>
                <th>Label</th>
                <th>Rule</th>
                <th>Logo</th>
              </tr>
            </thead>
            <tbody>
              {Object.entries(grounds).map(([name, g]) => (
                <tr key={name}>
                  <td>
                    <strong>{name}</strong>
                  </td>
                  <td><Swatch name={g.bg} color={tokens[g.bg] ?? g.bg} /></td>
                  <td><Swatch name={g.text} color={tokens[g.text] ?? g.text} /></td>
                  <td><Swatch name={g.label} color={tokens[g.label] ?? g.label} /></td>
                  <td><Swatch name={g.rule} color={tokens[g.rule] ?? g.rule} /></td>
                  <td>{g.logo}</td>
                </tr>
              ))}
            </tbody>
          </table>

          {Object.keys(accents).length > 0 && (
            <>
              <h3 className="poster-subhead">Accents</h3>
              <div className="poster-swatches">
                {Object.entries(accents).map(([name, a]) => (
                  <span key={name} className="poster-swatch">
                    <span className="poster-swatch-chip" style={{ background: tokens[a.fill] ?? a.fill, color: tokens[a.text] ?? a.text }}>
                      Aa
                    </span>
                    <span className="poster-swatch-name">{name}</span>
                    <code>
                      {a.fill} / {a.text}
                    </code>
                  </span>
                ))}
              </div>
              <p className="field-note">
                At most {b.accentRules.maxElements} accent element{b.accentRules.maxElements === 1 ? '' : 's'} per poster
                {b.accentRules.together ? '' : '; accents never together'}.
              </p>
            </>
          )}

          {(b.pairs ?? []).length > 0 && (
            <>
              <h3 className="poster-subhead">Approved pairs</h3>
              <div className="poster-pairs">
                {(b.pairs ?? []).map((p, i) => (
                  <span
                    key={i}
                    className="poster-pair"
                    style={{ background: tokens[p.bg] ?? p.bg, color: tokens[p.fg] ?? p.fg }}
                    title={`${p.fg} on ${p.bg}${p.minSizePx ? `, ${p.minSizePx}px and up` : ''}`}
                  >
                    {p.fg} on {p.bg}
                    {p.minSizePx ? <small> ≥{p.minSizePx}px</small> : null}
                  </span>
                ))}
              </div>
            </>
          )}

          <h3 className="poster-subhead">Type</h3>
          <table className="item-table poster-table">
            <tbody>
              {(['display', 'text', 'mono'] as const).map((role) => (
                <tr key={role}>
                  <td>
                    <strong>{role}</strong>
                  </td>
                  <td style={{ fontFamily: `"${b.fonts[role].family}", inherit` }}>{b.fonts[role].family || '—'}</td>
                  <td className="muted-cell">{(b.fonts[role].weights ?? []).join(', ')}</td>
                </tr>
              ))}
            </tbody>
          </table>

          <h3 className="poster-subhead">Canvases</h3>
          <table className="item-table poster-table">
            <thead>
              <tr>
                <th>Format</th>
                <th>Size</th>
                <th>Safe margins</th>
              </tr>
            </thead>
            <tbody>
              {Object.entries(formats).map(([name, f]) => (
                <tr key={name}>
                  <td>
                    <strong>{name}</strong>
                    {name === b.portraitFormat && <span className="badge"> on events</span>}
                  </td>
                  <td>
                    {f.width} × {f.height}
                  </td>
                  <td className="muted-cell">
                    {f.safe.top} / {f.safe.right} / {f.safe.bottom} / {f.safe.left}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}

      <div className="drawer-actions">
        <button type="button" className="btn btn-ghost" disabled={busy} onClick={onRederive}>
          Re-derive from the guide
        </button>
      </div>
    </section>
  );
}
