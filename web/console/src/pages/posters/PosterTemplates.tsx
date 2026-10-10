import { useCallback, useEffect, useMemo, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { api, type EventRecord, type PosterTemplate, type PosterTemplateStatus } from '../../api';
import { useSetChatContext } from '../../chatContext';
import { PostersTabs, errText, fmtDay } from './common';

// The template library. Every card is a real render with real content: the
// next upcoming event by default, or whichever event the select names, so
// a layout is judged on the copy it will actually carry.

type StatusFilter = '' | PosterTemplateStatus;

export function TemplateStatusBadges({ t }: { t: PosterTemplate }) {
  const builtin = !t.tenant_id;
  return (
    <span className="badge-row">
      <span className={`pill ${t.status === 'active' ? 'pill-ok' : t.status === 'archived' ? 'pill-error' : 'pill-off'}`}>
        {t.status}
      </span>
      {builtin && <span className="badge">built-in</span>}
      {t.hidden && <span className="badge badge-error">hidden</span>}
      {t.meta?.photos?.min > 0 && <span className="badge">needs photo</span>}
      <span className="badge">{t.origin}</span>
    </span>
  );
}

// TemplatePreview is one rendered image with a placeholder when the render
// is refused (422: the template needs a photo the library lacks, or the
// brand has a problem). The server's reason is JSON, not an image, so the
// browser fires onError and we cannot read it; the detail page can.
export function TemplatePreview({ src, alt, className }: { src: string; alt: string; className?: string }) {
  const [failed, setFailed] = useState(false);
  useEffect(() => setFailed(false), [src]);
  if (failed) {
    return (
      <div className={`poster-tpl-blank ${className ?? ''}`} title={alt}>
        <span>Can't render yet</span>
      </div>
    );
  }
  return <img className={className} src={src} alt={alt} loading="lazy" onError={() => setFailed(true)} />;
}

export default function PosterTemplates() {
  const [templates, setTemplates] = useState<PosterTemplate[] | null>(null);
  const [events, setEvents] = useState<EventRecord[]>([]);
  const [eventId, setEventId] = useState('');
  const [status, setStatus] = useState<StatusFilter>('');
  const [needsPhoto, setNeedsPhoto] = useState(false);
  const [origin, setOrigin] = useState('');
  const [name, setName] = useState('');
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const navigate = useNavigate();

  const load = useCallback(() => {
    api
      .listPosterTemplates()
      .then((r) => setTemplates(r.templates ?? []))
      .catch((e) => setErr(errText(e)));
  }, []);
  useEffect(load, [load]);
  useEffect(() => {
    api
      .listEvents({})
      .then((r) => setEvents(r.events ?? []))
      .catch(() => setEvents([]));
  }, []);
  useSetChatContext('the Poster templates page', load);

  const origins = useMemo(
    () => [...new Set((templates ?? []).map((t) => t.origin).filter(Boolean))].sort(),
    [templates],
  );
  const shown = (templates ?? []).filter(
    (t) =>
      (!status || t.status === status) &&
      (!needsPhoto || t.meta?.photos?.min > 0) &&
      (!origin || t.origin === origin),
  );

  const create = async () => {
    setBusy(true);
    setErr(null);
    try {
      const r = await api.createPosterTemplate(name.trim() ? { name: name.trim() } : {});
      navigate(`/posters/templates/${r.template.id}`);
    } catch (e) {
      setErr(errText(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <Link to="/posters">Posters</Link>
          <span className="crumb-sep">/</span>
          <span>Templates</span>
        </nav>
        <h1>Templates</h1>
        <p className="page-sub">
          The layouts the generator spreads across. Active ones are offered; drafts are
          being worked on; archived ones are kept for the posters that used them.
        </p>
      </div>
      <PostersTabs />

      {err && <p className="banner banner-error">{err}</p>}

      <div className="toolbar">
        <label className="check">
          Preview with
          <select value={eventId} onChange={(e) => setEventId(e.target.value)}>
            <option value="">next upcoming event</option>
            {events.map((ev) => (
              <option key={ev.id} value={ev.id}>
                {ev.title}
              </option>
            ))}
          </select>
        </label>
        <select value={status} onChange={(e) => setStatus(e.target.value as StatusFilter)} aria-label="Status">
          <option value="">All statuses</option>
          <option value="active">Active</option>
          <option value="draft">Draft</option>
          <option value="archived">Archived</option>
        </select>
        {origins.length > 1 && (
          <select value={origin} onChange={(e) => setOrigin(e.target.value)} aria-label="Origin">
            <option value="">Any origin</option>
            {origins.map((o) => (
              <option key={o} value={o}>
                {o}
              </option>
            ))}
          </select>
        )}
        <label className="check">
          <input type="checkbox" checked={needsPhoto} onChange={(e) => setNeedsPhoto(e.target.checked)} />
          Needs a photo
        </label>
        <span className="toolbar-end inline-form">
          <input
            placeholder="New template name (optional)"
            value={name}
            onChange={(e) => setName(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && !busy && create()}
          />
          <button type="button" className="btn" disabled={busy} onClick={create}>
            New template
          </button>
        </span>
      </div>

      {templates === null && !err && <p className="muted">Loading…</p>}
      {templates && shown.length === 0 && <p className="empty">No templates match.</p>}

      <div className="poster-tpl-grid">
        {shown.map((t) => (
          <Link key={t.id} to={`/posters/templates/${t.id}`} className="poster-tpl-card">
            <TemplatePreview
              src={api.posterTemplateRenderURL(t.id, { event_id: eventId || undefined, v: t.updated_at })}
              alt={`${t.name} preview`}
            />
            <div className="poster-tpl-body">
              <div className="card-title">{t.name}</div>
              <TemplateStatusBadges t={t} />
              {t.description && <div className="card-desc">{t.description}</div>}
              <div className="card-detail">
                {t.picks} {t.picks === 1 ? 'pick' : 'picks'}
                {t.last_used && <> · last used {fmtDay(t.last_used)}</>}
              </div>
              {t.edits && t.edits.length > 0 && (
                <div className="card-detail">Usually edited to: {t.edits.join('; ')}</div>
              )}
            </div>
          </Link>
        ))}
      </div>
    </div>
  );
}
