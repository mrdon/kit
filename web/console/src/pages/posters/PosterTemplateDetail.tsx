import { useCallback, useEffect, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { api, type PosterTemplateDetail as Payload } from '../../api';
import { useSetChatContext } from '../../chatContext';
import { SLUG } from '../../workspace';
import PosterChat from './PosterChat';
import { TemplatePreview, TemplateStatusBadges } from './PosterTemplates';
import { errText, fmtWhen } from './common';

// One template: previews at every brand format, the reference it came from,
// its version history with roll back, lifecycle actions, and template chat.
// Built-ins cannot be edited in place; duplicate one to change it.

export default function PosterTemplateDetail() {
  const { id = '' } = useParams();
  const [d, setD] = useState<Payload | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [renaming, setRenaming] = useState(false);
  const [name, setName] = useState('');
  // A cache-buster for the previews: bumped on every change this page makes.
  const [rev, setRev] = useState(0);
  const navigate = useNavigate();

  const load = useCallback(() => {
    if (!id) return;
    api
      .getPosterTemplate(id)
      .then((r) => {
        setD(r);
        setErr(null);
        setRev((n) => n + 1);
      })
      .catch((e) => setErr(errText(e)));
  }, [id]);
  useEffect(load, [load]);

  const t = d?.template;
  useSetChatContext(t ? `the Poster templates page, viewing "${t.name}"` : 'a poster template page', load);

  const guard = async (fn: () => Promise<void>) => {
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

  const setStatus = (status: string, said: string) =>
    guard(async () => {
      await api.setPosterTemplateStatus(id, status);
      setNote(said);
      load();
    });

  const duplicate = () =>
    guard(async () => {
      const r = await api.createPosterTemplate({ parent_template_id: id });
      navigate(`/posters/templates/${r.template.id}`);
    });

  const rename = () =>
    guard(async () => {
      await api.renamePosterTemplate(id, name);
      setRenaming(false);
      setNote('Renamed.');
      load();
    });

  const rollback = (vid: string) =>
    guard(async () => {
      await api.rollbackPosterTemplate(id, vid);
      setNote('Rolled back. The old source is the newest version now.');
      load();
    });

  const builtin = !!t && !t.tenant_id;
  const formats = d?.formats?.length ? d.formats : [''];

  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <Link to="/posters">Posters</Link>
          <span className="crumb-sep">/</span>
          <Link to="/posters/templates">Templates</Link>
          <span className="crumb-sep">/</span>
          <span>{t?.name ?? '…'}</span>
        </nav>
        <div className="page-head-row">
          {renaming && t ? (
            <form
              className="inline-form"
              onSubmit={(e) => {
                e.preventDefault();
                rename();
              }}
            >
              <input value={name} onChange={(e) => setName(e.target.value)} autoFocus aria-label="Template name" />
              <button type="submit" className="btn btn-sm" disabled={busy || !name.trim()}>
                Save
              </button>
              <button type="button" className="btn btn-ghost btn-sm" onClick={() => setRenaming(false)}>
                Cancel
              </button>
            </form>
          ) : (
            <h1>{t?.name ?? 'Template'}</h1>
          )}
          {t && <TemplateStatusBadges t={t} />}
        </div>
        {t?.description && <p className="page-sub">{t.description}</p>}
      </div>

      {note && (
        <p className="banner banner-ok" onClick={() => setNote(null)}>
          {note}
        </p>
      )}
      {err && <p className="banner banner-error">{err}</p>}
      {!d && !err && <p className="muted">Loading…</p>}

      {d && t && (
        <div className="poster-page">
          <div className="poster-main">
            <div className="poster-actions">
              {builtin ? (
                t.hidden ? (
                  <button type="button" className="btn" disabled={busy} onClick={() => setStatus('visible', 'Offered again.')}>
                    Show
                  </button>
                ) : (
                  <button type="button" className="btn btn-ghost" disabled={busy} onClick={() => setStatus('hidden', 'Hidden from the generator.')}>
                    Hide
                  </button>
                )
              ) : (
                <>
                  {t.status !== 'active' && (
                    <button type="button" className="btn" disabled={busy} onClick={() => setStatus('active', 'Active: the generator will offer it.')}>
                      Activate
                    </button>
                  )}
                  {t.status !== 'draft' && (
                    <button type="button" className="btn btn-ghost" disabled={busy} onClick={() => setStatus('draft', 'Back to draft.')}>
                      Make draft
                    </button>
                  )}
                  {t.status !== 'archived' && (
                    <button type="button" className="btn btn-ghost" disabled={busy} onClick={() => setStatus('archived', 'Archived.')}>
                      Archive
                    </button>
                  )}
                  <button
                    type="button"
                    className="btn btn-ghost"
                    disabled={busy}
                    onClick={() => {
                      setName(t.name);
                      setRenaming(true);
                    }}
                  >
                    Rename
                  </button>
                </>
              )}
              <button type="button" className="btn btn-ghost" disabled={busy} onClick={duplicate}>
                Duplicate
              </button>
            </div>
            {builtin && (
              <p className="field-note">
                A built-in ships with Kit and cannot be edited in place. Duplicate it to make
                your own version.
              </p>
            )}

            <section className="poster-section">
              <h2 className="panel-title">Previews</h2>
              <div className="poster-tpl-previews">
                {formats.map((f) => (
                  <figure key={f || 'default'}>
                    <TemplatePreview
                      src={api.posterTemplateRenderURL(id, { format: f || undefined, v: `${t.updated_at}-${rev}` })}
                      alt={`${t.name} at ${f || 'the default format'}`}
                    />
                    <figcaption>{f || 'portrait'}</figcaption>
                  </figure>
                ))}
              </div>
            </section>

            {t.reference_attachment_id && (
              <section className="poster-section">
                <h2 className="panel-title">Reference</h2>
                <p className="field-note">The image this template was made from.</p>
                <div className="poster-tpl-reference">
                  <img src={api.posterTemplateReferenceURL(id)} alt={`Reference for ${t.name}`} />
                </div>
              </section>
            )}

            <section className="poster-section">
              <h2 className="panel-title">History</h2>
              <ul className="timeline poster-tpl-history">
                {d.versions.map((v) => {
                  const current = v.id === t.current_version_id;
                  return (
                    <li key={v.id} className="timeline-item">
                      <div className="timeline-head">
                        <strong>{v.summary || 'Edited'}</strong>
                        {current && <span className="badge">current</span>}
                        <span className="timeline-meta">
                          {v.author} · {fmtWhen(v.created_at)}
                        </span>
                        {!current && !builtin && (
                          <button type="button" className="btn btn-ghost btn-sm" disabled={busy} onClick={() => rollback(v.id)}>
                            Roll back
                          </button>
                        )}
                      </div>
                      <details className="policy-editor">
                        <summary>Source</summary>
                        <pre className="poster-source">{v.source}</pre>
                      </details>
                    </li>
                  );
                })}
              </ul>
            </section>
          </div>

          <PosterChat
            executeUrl={`/${SLUG}/api/posters/templates/${id}/chat/execute`}
            title="Edit with Kit"
            hint={
              builtin
                ? 'Chat can describe this layout; duplicate it to change it.'
                : 'Describe the layout you want: "put the photo on the left third", "bigger title, no summary".'
            }
            placeholder="What should the layout do?"
            onDone={load}
          />
        </div>
      )}
    </div>
  );
}
