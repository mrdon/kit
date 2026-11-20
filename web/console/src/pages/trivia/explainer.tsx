import { useEffect, useState } from 'react';
import { api, type TriviaExplainer } from '../../api';

// The lobby video: a how-to-play loop the TV shows beside the join code until
// the game starts. It lives in Google Drive so the venue can replace it
// without touching Kit; Kit fetches a copy when the link is saved, so a file
// that isn't shared publicly is caught here rather than on quiz night.
export function ExplainerPanel() {
  const [st, setSt] = useState<TriviaExplainer | null>(null);
  const [draft, setDraft] = useState('');
  const [err, setErr] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api
      .triviaExplainer()
      .then((s) => {
        setSt(s);
        setDraft(s.link);
      })
      .catch((e) => setErr((e as Error).message));
  }, []);

  const save = async (link: string) => {
    setBusy(true);
    setErr(null);
    setNote(null);
    try {
      const s = await api.saveTriviaExplainer(link);
      setSt(s);
      setDraft(s.link);
      setNote(
        s.link
          ? `Video saved (${(s.size_mb ?? 0).toFixed(1)} MB). The TV shows it in the lobby.`
          : 'Video removed. The lobby shows the rules instead.',
      );
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const dirty = st !== null && draft.trim() !== st.link;
  return (
    <section className="panel">
      <h2>Lobby video</h2>
      <p className="page-sub">
        A how-to-play video for the TV&rsquo;s join screen. It loops beside the QR code until the
        game starts, in place of the written rules.
      </p>
      {err ? <p className="banner banner-error">{err}</p> : null}
      {note ? <p className="banner banner-ok">{note}</p> : null}
      {st ? (
        <>
          <label className="field">
            <span>Google Drive link</span>
            <input
              type="url"
              value={draft}
              disabled={busy}
              placeholder="https://drive.google.com/file/d/…/view"
              onChange={(e) => setDraft(e.target.value)}
            />
            <span className="field-note">
              An .mp4 shared as <strong>Anyone with the link</strong>. To change the video later,
              upload a new version of the same file in Drive (File &rarr; Manage versions); the TV
              picks it up within the hour, and the link stays the same.
            </span>
          </label>
          <div style={{ display: 'flex', gap: '0.5rem' }}>
            <button
              type="button"
              className="btn"
              disabled={busy || !dirty || draft.trim() === ''}
              onClick={() => void save(draft)}
            >
              {busy ? 'Fetching from Drive…' : 'Save'}
            </button>
            {st.link ? (
              <button
                type="button"
                className="btn btn-ghost"
                disabled={busy}
                onClick={() => void save('')}
              >
                Remove video
              </button>
            ) : null}
          </div>
        </>
      ) : null}
    </section>
  );
}
