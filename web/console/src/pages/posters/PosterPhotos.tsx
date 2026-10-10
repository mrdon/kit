import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type PosterPhoto, type PosterPhotoStatus, type PosterPhotosPayload } from '../../api';
import { useSetChatContext } from '../../chatContext';
import { PostersTabs, errText } from './common';

// The photo index. Kit stores descriptions, not images: Drive holds the
// files. Descriptions are normally written from Claude Code over MCP; this
// page is for correcting one by hand, and for the focus point, which is
// quicker to click than to describe.

type StatusFilter = '' | PosterPhotoStatus;

export default function PosterPhotos() {
  const [data, setData] = useState<PosterPhotosPayload | null>(null);
  const [status, setStatus] = useState<StatusFilter>('');
  const [folder, setFolder] = useState('');
  const [openId, setOpenId] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const load = useCallback(() => {
    api
      .listPosterPhotos({ status, folder })
      .then((r) => {
        setData(r);
        setErr(null);
      })
      .catch((e) => setErr(errText(e)));
  }, [status, folder]);
  useEffect(load, [load]);
  useSetChatContext('the Poster photos page', load);

  const counts = data?.counts;
  const replace = (p: PosterPhoto) =>
    setData((d) => (d ? { ...d, photos: d.photos.map((x) => (x.id === p.id ? p : x)) } : d));

  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <Link to="/posters">Posters</Link>
          <span className="crumb-sep">/</span>
          <span>Photos</span>
        </nav>
        <h1>Photos</h1>
        <p className="page-sub">
          What Kit knows about each photo in the shared folder. The description and focus
          point drive which photo a poster gets, so fix them here when a choice looks off.
        </p>
      </div>
      <PostersTabs />

      {err && <p className="banner banner-error">{err}</p>}
      {counts && counts.pending > 0 && (
        <p className="banner banner-ok">
          {counts.pending} {counts.pending === 1 ? 'photo is' : 'photos are'} waiting for
          descriptions. Index them from Claude Code over MCP.
        </p>
      )}

      {counts && (
        <div className="filter-chips">
          {(
            [
              ['', 'All', counts.pending + counts.indexed + counts.removed],
              ['pending', 'Pending', counts.pending],
              ['indexed', 'Indexed', counts.indexed],
              ['removed', 'Removed', counts.removed],
            ] as [StatusFilter, string, number][]
          ).map(([v, label, n]) => (
            <button
              key={v}
              type="button"
              className={`chip${status === v ? ' chip-active' : ''}`}
              onClick={() => setStatus(v)}
            >
              {label} <span className="chip-count">{n}</span>
            </button>
          ))}
          {data && data.folders.length > 1 && (
            <select className="mini-select" value={folder} onChange={(e) => setFolder(e.target.value)} aria-label="Folder">
              <option value="">All folders</option>
              {data.folders.map((f) => (
                <option key={f} value={f}>
                  {f || '(root)'}
                </option>
              ))}
            </select>
          )}
        </div>
      )}

      {!data && !err && <p className="muted">Loading…</p>}
      {data && data.photos.length === 0 && (
        <p className="empty">
          No photos here. An admin sets the Drive folder under{' '}
          <Link to="/admin/posters">Posters settings</Link> and syncs it, or a harness runs sync_poster_photos.
        </p>
      )}

      <div className="poster-photo-grid">
        {(data?.photos ?? []).map((p) =>
          openId === p.id ? (
            <PhotoEditor key={p.id} photo={p} onClose={() => setOpenId(null)} onSaved={replace} />
          ) : (
            <button key={p.id} type="button" className="poster-photo" onClick={() => setOpenId(p.id)}>
              <PhotoImage photo={p} size={400} />
              <span className="poster-photo-meta">
                <span className={`pill ${p.status === 'indexed' ? 'pill-ok' : p.status === 'removed' ? 'pill-error' : 'pill-off'}`}>
                  {p.status}
                </span>
                {p.c2pa === 'ai' && <span className="badge badge-error">AI-made</span>}
                <span className="poster-photo-name" title={`${p.folder}/${p.filename}`}>
                  {p.filename}
                </span>
              </span>
              {p.description && <span className="card-desc poster-photo-desc">{p.description}</span>}
            </button>
          ),
        )}
      </div>
    </div>
  );
}

// PhotoImage tries Drive's thumbnail first and falls back to Kit's proxy,
// which goes through the renderer's cache (Drive refuses some thumbnails).
function PhotoImage({
  photo,
  size,
  onClick,
  children,
}: {
  photo: PosterPhoto;
  size: number;
  onClick?: (e: React.MouseEvent<HTMLImageElement>) => void;
  children?: React.ReactNode;
}) {
  const [src, setSrc] = useState(photo.thumb || api.posterPhotoImageURL(photo.id, size));
  useEffect(() => setSrc(photo.thumb || api.posterPhotoImageURL(photo.id, size)), [photo.thumb, photo.id, size]);
  return (
    <span className="poster-photo-img">
      <img
        src={src}
        alt={photo.description || photo.filename}
        loading="lazy"
        referrerPolicy="no-referrer"
        onError={() => {
          const fallback = api.posterPhotoImageURL(photo.id, size);
          if (src !== fallback) setSrc(fallback);
        }}
        onClick={onClick}
        style={onClick ? { cursor: 'crosshair' } : undefined}
      />
      {children}
    </span>
  );
}

function PhotoEditor({
  photo,
  onClose,
  onSaved,
}: {
  photo: PosterPhoto;
  onClose: () => void;
  onSaved: (p: PosterPhoto) => void;
}) {
  const [description, setDescription] = useState(photo.description);
  const [tags, setTags] = useState(photo.tags.join(', '));
  const [notes, setNotes] = useState(photo.notes);
  const [focus, setFocus] = useState({ x: photo.focus_x, y: photo.focus_y });
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const setFocusFromClick = (e: React.MouseEvent<HTMLImageElement>) => {
    const r = e.currentTarget.getBoundingClientRect();
    setFocus({
      x: Math.min(1, Math.max(0, (e.clientX - r.left) / r.width)),
      y: Math.min(1, Math.max(0, (e.clientY - r.top) / r.height)),
    });
  };

  const save = async () => {
    setBusy(true);
    setErr(null);
    try {
      const r = await api.updatePosterPhoto(photo.id, {
        description: description.trim(),
        tags: tags
          .split(',')
          .map((t) => t.trim())
          .filter(Boolean),
        notes: notes.trim(),
        focus_x: focus.x,
        focus_y: focus.y,
      });
      onSaved(r.photo);
      onClose();
    } catch (e) {
      setErr(errText(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="poster-photo poster-photo-open">
      <PhotoImage photo={photo} size={1024} onClick={setFocusFromClick}>
        <span className="poster-focus-dot" style={{ left: `${focus.x * 100}%`, top: `${focus.y * 100}%` }} aria-hidden />
      </PhotoImage>
      <div className="poster-photo-form">
        <div className="card-detail">
          {photo.folder}/{photo.filename} · {photo.width}×{photo.height} {photo.orientation}
          {photo.c2pa && photo.c2pa !== 'none' && <> · C2PA: {photo.c2pa}</>}
        </div>
        <p className="field-note">Click the photo to set the focus point: the part a crop must keep.</p>
        <label className="field">
          <span>Description</span>
          <textarea rows={3} value={description} onChange={(e) => setDescription(e.target.value)} />
        </label>
        <label className="field">
          <span>Tags (comma separated)</span>
          <input value={tags} onChange={(e) => setTags(e.target.value)} />
        </label>
        <label className="field">
          <span>Notes</span>
          <input value={notes} onChange={(e) => setNotes(e.target.value)} placeholder="Faces prominent, clean space top-left…" />
        </label>
        {err && <p className="field-hint">{err}</p>}
        <div className="drawer-actions">
          <button type="button" className="btn" disabled={busy} onClick={save}>
            {busy ? 'Saving…' : 'Save'}
          </button>
          <button type="button" className="btn btn-ghost" disabled={busy} onClick={onClose}>
            Cancel
          </button>
        </div>
      </div>
    </div>
  );
}
