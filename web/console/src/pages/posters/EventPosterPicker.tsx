import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  api,
  generatePosterOptions,
  type EventRecord,
  type PosterForEvent,
  type PosterGenerateStage,
  type PosterOption,
  type PosterVersion,
} from '../../api';
import { OptionGrid, errText, stageText } from './common';

// The simple picker in the event drawer: generate a batch, click one, it is
// the event's poster. No chat here; "Open in Posters" is for anything more.
//
// It owns its own API calls and state. The drawer only learns that the event
// changed (onChanged), the same way the upload and remove buttons tell it.
export default function EventPosterPicker({
  eventId,
  onChanged,
}: {
  eventId: string;
  onChanged: (msg: string, next?: EventRecord) => void;
}) {
  const [state, setState] = useState<PosterForEvent | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [stage, setStage] = useState<PosterGenerateStage | null>(null);
  const [generating, setGenerating] = useState(false);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<string | null>(null);
  // The version Update poster made, offered beside the current one until it
  // is set on the event (or the next reload replaces it).
  const [updated, setUpdated] = useState<PosterVersion | null>(null);

  const load = useCallback(() => {
    api
      .posterForEvent(eventId)
      .then((r) => {
        setState(r);
        setErr(null);
      })
      .catch((e) => setErr(errText(e)));
  }, [eventId]);
  useEffect(load, [load]);

  const poster = state?.poster ?? null;
  const options = state?.options ?? [];

  // Refetch the event so the drawer's existing preview picks up the new
  // hero_attachment_id; onChanged with no event would close the drawer.
  const refreshEvent = async (msg: string) => {
    const r = await api.getEvent(eventId);
    onChanged(msg, r.event);
  };

  const generate = async (more: boolean) => {
    setGenerating(true);
    setErr(null);
    setNote(null);
    setStage(null);
    try {
      const r = await generatePosterOptions(eventId, more, setStage);
      setState((s) => ({
        poster: r.poster,
        options: r.options,
        renderer_ready: s?.renderer_ready ?? true,
        brand_ready: s?.brand_ready ?? true,
        brand_problem: s?.brand_problem,
      }));
      if (r.options.length === 0) {
        setNote('No options came out of this run. Check the templates and photo index.');
      } else if (r.no_photo) {
        setNote('No photo in the library honestly fits this event, so these are type-only layouts.');
      }
    } catch (e) {
      setErr(errText(e));
    } finally {
      setGenerating(false);
      setStage(null);
    }
  };

  const guard = async (fn: () => Promise<void>) => {
    setBusy(true);
    setErr(null);
    try {
      await fn();
    } catch (e) {
      setErr(errText(e));
    } finally {
      setBusy(false);
    }
  };

  const pick = (o: PosterOption | PosterVersion) =>
    guard(async () => {
      if (!poster) return;
      await api.pickPoster(poster.id, o.id);
      setUpdated(null);
      load();
      await refreshEvent('Poster set on the event.');
    });

  const updateFacts = () =>
    guard(async () => {
      if (!poster) return;
      const r = await api.updatePosterFacts(poster.id);
      if (!r.version) {
        setErr(r.problems?.join('; ') || 'The poster could not be updated.');
        return;
      }
      setUpdated(r.version);
      const changed = (r.changes ?? []).join(', ');
      setNote(changed ? `Updated: ${changed}.` : 'Updated to the event as it is now.');
    });

  if (!state && !err) return <p className="field-note">Loading poster options…</p>;

  const canGenerate = !!state?.brand_ready && !!state?.renderer_ready && !generating && !busy;
  const current = poster?.current_version_id;

  return (
    <div className="poster-picker">
      {state && !state.brand_ready && (
        <p className="field-note">
          Posters need a brand first.{' '}
          {state.brand_problem || 'Add a branding-guide skill to this workspace.'}
        </p>
      )}
      {state && state.brand_ready && !state.renderer_ready && (
        <p className="field-note">The poster renderer is starting; try again in a moment.</p>
      )}

      {poster?.stale && (
        <div className="banner banner-error poster-stale">
          <span>
            <strong>Out of date.</strong> The event changed since this poster was set.
          </span>
          <button type="button" className="btn btn-sm" disabled={busy || generating} onClick={updateFacts}>
            {busy ? 'Updating…' : 'Update poster'}
          </button>
        </div>
      )}

      {updated && poster && current && (
        <div className="poster-compare">
          <figure>
            <img src={api.posterRenderURL(poster.id, poster.set_version_id ?? current)} alt="Current poster" />
            <figcaption>On the event</figcaption>
          </figure>
          <figure>
            <img src={api.posterRenderURL(poster.id, updated.id)} alt="Updated poster" />
            <figcaption>
              Updated{' '}
              <button type="button" className="btn btn-sm" disabled={busy} onClick={() => pick(updated)}>
                Set on event
              </button>
            </figcaption>
          </figure>
        </div>
      )}

      <div className="poster-picker-actions">
        <button
          type="button"
          className="btn btn-ghost"
          disabled={!canGenerate}
          onClick={() => generate(options.length > 0)}
        >
          {generating ? stageText(stage) : options.length > 0 ? 'More options' : 'Generate poster'}
        </button>
        {poster && (
          <Link to={`/posters/${poster.id}`} className="btn btn-ghost">
            Open in Posters
          </Link>
        )}
      </div>

      {note && <p className="field-note">{note}</p>}
      {err && <p className="field-hint">{err}</p>}

      <OptionGrid options={options} setId={poster?.set_version_id} busy={busy || generating} onPick={pick} />
    </div>
  );
}
