import { useCallback, useEffect, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import {
  PosterAuthor,
  api,
  generatePosterOptions,
  type PosterDetail as PosterDetailPayload,
  type PosterGenerateStage,
  type PosterOption,
  type PosterTemplate,
} from '../../api';
import { useSetChatContext } from '../../chatContext';
import { SLUG } from '../../workspace';
import PosterChat from './PosterChat';
import { OptionGrid, errText, fmtWhen, stageText, statusPill } from './common';

// The poster page: the current version large, its history as a strip, the
// batch of options when there is one, every brand format to download, and a
// chat panel for any change. Chat is the editor; there is no copy form.

// versionLabel is the one line under a strip thumbnail: what the edit was,
// or who made it when there was no instruction (an option, a pick).
function versionLabel(instruction: string, author: PosterAuthor): string {
  const text = instruction.trim() || (author === PosterAuthor.System ? 'Generated option' : `${author} edit`);
  return text.length > 48 ? `${text.slice(0, 46)}…` : text;
}

export default function PosterDetail() {
  const { id = '' } = useParams();
  const [d, setD] = useState<PosterDetailPayload | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [generating, setGenerating] = useState(false);
  const [stage, setStage] = useState<PosterGenerateStage | null>(null);
  const [options, setOptions] = useState<PosterOption[] | null>(null);
  const [savedTemplate, setSavedTemplate] = useState<PosterTemplate | null>(null);
  const [previewFormat, setPreviewFormat] = useState<string | null>(null);

  const load = useCallback(() => {
    if (!id) return;
    api
      .getPoster(id)
      .then((r) => {
        setD(r);
        setErr(null);
      })
      .catch((e) => setErr(errText(e)));
  }, [id]);
  useEffect(load, [load]);

  const poster = d?.poster;
  useSetChatContext(
    poster ? `the Posters page, viewing the poster for "${poster.event_title || poster.title}"` : 'a poster page',
    load,
  );

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

  const setCurrent = (vid: string) =>
    guard(async () => {
      await api.setPosterCurrent(id, vid);
      load();
    });

  const pick = (vid: string) =>
    guard(async () => {
      await api.pickPoster(id, vid);
      setNote('Set on the event.');
      setOptions(null);
      load();
    });

  const updateFacts = () =>
    guard(async () => {
      const r = await api.updatePosterFacts(id);
      if (!r.version) {
        setErr(r.problems?.join('; ') || 'The poster could not be updated.');
        return;
      }
      const changed = (r.changes ?? []).join(', ');
      setNote(
        (changed ? `Updated: ${changed}. ` : 'Updated to the event as it is now. ') +
          'It is the current version; press Set on event when it looks right.',
      );
      load();
    });

  const saveTemplate = () =>
    guard(async () => {
      const r = await api.savePosterAsTemplate(id);
      setSavedTemplate(r.template);
      setNote('Saved as a draft template.');
    });

  const newOptions = async () => {
    if (!poster?.event_id) return;
    setGenerating(true);
    setErr(null);
    setNote(null);
    try {
      const r = await generatePosterOptions(poster.event_id, true, setStage);
      setOptions(r.options);
      if (r.fresh_copy) setNote('The event changed since the last batch, so the copy was rewritten.');
      else if (r.options.length === 0) setNote('No new options came out of this run.');
      load();
    } catch (e) {
      setErr(errText(e));
    } finally {
      setGenerating(false);
      setStage(null);
    }
  };

  const title = poster?.event_title || poster?.title || 'Poster';
  const currentId = poster?.current_version_id;
  const shownOptions = options ?? d?.options ?? [];
  const currentIsSet = !!currentId && currentId === poster?.set_version_id;

  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <Link to="/posters">Posters</Link>
          <span className="crumb-sep">/</span>
          <span>{title}</span>
        </nav>
        <div className="page-head-row">
          <h1>{title}</h1>
          {poster && <span className={statusPill(poster.status)}>{poster.status}</span>}
          {poster?.set_version_id && currentId && !currentIsSet && (
            <span className="pill pill-off" title="The version shown is an edit; the event still has the one marked on event">
              current version not on the event
            </span>
          )}
        </div>
        {poster?.event_id && (
          <p className="page-sub">
            For the event <Link to={`/events/${poster.event_id}`}>{poster.event_title || poster.title}</Link>.
          </p>
        )}
      </div>

      {note && (
        <p className="banner banner-ok" onClick={() => setNote(null)}>
          {note}
          {savedTemplate && (
            <>
              {' '}
              <Link to={`/posters/templates/${savedTemplate.id}`}>Open "{savedTemplate.name}"</Link>
            </>
          )}
        </p>
      )}
      {err && <p className="banner banner-error">{err}</p>}
      {!d && !err && <p className="muted">Loading…</p>}
      {d && !d.renderer_ready && (
        <p className="banner banner-error">The poster renderer is not running; renders will fail until it is back.</p>
      )}

      {d && poster && (
        <div className="poster-page">
          <div className="poster-main">
            {poster.stale && (
              <div className="banner banner-error poster-stale">
                <span>
                  <strong>Out of date.</strong> The event changed since this poster was set.
                </span>
                <button type="button" className="btn btn-sm" disabled={busy || generating} onClick={updateFacts}>
                  {busy ? 'Updating…' : 'Update poster'}
                </button>
              </div>
            )}

            <div className="poster-hero">
              {currentId ? (
                <img
                  key={currentId}
                  src={api.posterRenderURL(id, currentId)}
                  alt={d.current?.content?.title || title}
                />
              ) : (
                <p className="empty">This poster has no version yet. Generate options to start.</p>
              )}
            </div>

            <div className="poster-actions">
              <button
                type="button"
                className="btn"
                disabled={busy || generating || !currentId || currentIsSet}
                onClick={() => currentId && pick(currentId)}
                title={currentIsSet ? 'This version is already on the event' : undefined}
              >
                {currentIsSet ? 'On the event' : 'Set on event'}
              </button>
              <button
                type="button"
                className="btn btn-ghost"
                disabled={busy || generating || !poster.event_id || !d.renderer_ready}
                onClick={newOptions}
              >
                {generating ? stageText(stage) : 'New options'}
              </button>
              <button
                type="button"
                className="btn btn-ghost"
                disabled={busy || generating || !currentId}
                onClick={saveTemplate}
              >
                Save as template
              </button>
            </div>

            {d.current && d.current.problems.length > 0 && (
              <p className="field-hint">{d.current.problems.join('; ')}</p>
            )}

            {d.versions.length > 0 && (
              <section className="poster-section">
                <h2 className="panel-title">Versions</h2>
                <p className="field-note">
                  Newest first. Click one to make it current; the next edit starts from it.
                </p>
                <div className="poster-strip">
                  {d.versions.map((v) => {
                    const isCurrent = v.id === currentId;
                    const isSet = v.id === poster.set_version_id;
                    return (
                      <button
                        key={v.id}
                        type="button"
                        className={`poster-strip-item${isCurrent ? ' poster-strip-current' : ''}`}
                        disabled={busy || isCurrent}
                        onClick={() => setCurrent(v.id)}
                        title={v.instruction || v.author}
                      >
                        <img src={v.thumb} alt={v.instruction || `${v.author} version`} loading="lazy" />
                        <span className="poster-strip-meta">
                          {fmtWhen(v.created_at)}
                          {isSet && <span className="badge">on event</span>}
                        </span>
                        <span className="poster-strip-label">{versionLabel(v.instruction, v.author)}</span>
                      </button>
                    );
                  })}
                </div>
              </section>
            )}

            {shownOptions.length > 0 && (
              <section className="poster-section">
                <h2 className="panel-title">Options</h2>
                <p className="field-note">
                  Click one to make it current and edit from there. Nothing reaches the event until you
                  press Set on event.
                </p>
                <OptionGrid
                  options={shownOptions}
                  setId={poster.set_version_id}
                  currentId={currentId}
                  busy={busy || generating}
                  pickLabel="Make current"
                  onPick={(o) => setCurrent(o.id)}
                />
              </section>
            )}

            {currentId && d.formats.length > 0 && (
              <section className="poster-section">
                <h2 className="panel-title">Formats</h2>
                <p className="field-note">
                  The current version at each size the brand guide defines. Only{' '}
                  {d.portrait_format || 'portrait'} goes on the event.
                </p>
                <div className="poster-formats">
                  {d.formats.map((f) => (
                    <span key={f} className="poster-format">
                      <button
                        type="button"
                        className={`chip${previewFormat === f ? ' chip-active' : ''}`}
                        onClick={() => setPreviewFormat(previewFormat === f ? null : f)}
                      >
                        {f}
                      </button>
                      <a
                        className="poster-format-dl"
                        href={api.posterRenderURL(id, currentId, f, true)}
                        target="_blank"
                        rel="noopener"
                      >
                        Download
                      </a>
                    </span>
                  ))}
                </div>
                {previewFormat && (
                  <div className="poster-format-preview">
                    <img
                      key={`${currentId}-${previewFormat}`}
                      src={api.posterRenderURL(id, currentId, previewFormat)}
                      alt={`${title} (${previewFormat})`}
                    />
                  </div>
                )}
              </section>
            )}
          </div>

          <PosterChat
            executeUrl={`/${SLUG}/api/posters/${id}/chat/execute`}
            title="Edit with Kit"
            hint='Any change, copy included: "fix the typo in the title", "move the photo left", "make it feel like autumn".'
            placeholder="What should change?"
            onDone={load}
          />
        </div>
      )}
    </div>
  );
}
