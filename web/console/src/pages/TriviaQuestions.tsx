import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import { API_BASE, api, type BuiltinPack, type Dataset, type ImportReport } from '../api';
import { useSetChatContext } from '../chatContext';

// Managing the workspace's question sets: add a set Kit ships, upload your
// own, rename one, delete one.
//
// This lives in Admin rather than on a game's setup page because a set
// belongs to the WORKSPACE and outlives any one night — a Christmas pack is
// still there next December. Only the choice of which sets a game draws from
// is per game, and that stays on the setup page next to the board.
export default function TriviaQuestions() {
  useSetChatContext('the Trivia questions page');
  const [datasets, setDatasets] = useState<Dataset[]>([]);
  const [packs, setPacks] = useState<BuiltinPack[]>([]);
  const [report, setReport] = useState<ImportReport | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [name, setName] = useState('');
  const [renaming, setRenaming] = useState<string | null>(null);
  const [draft, setDraft] = useState('');
  const [confirming, setConfirming] = useState<string | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  const load = () => {
    api.triviaQuestions()
      .then((r) => {
        setDatasets(r.datasets ?? []);
        setPacks(r.packs ?? []);
      })
      .catch((e) => setErr(e.message));
  };
  useEffect(load, []);

  const run = async (fn: () => Promise<void>) => {
    setBusy(true);
    setErr(null);
    try {
      await fn();
      load();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const upload = (file: File) =>
    run(async () => {
      const form = new FormData();
      form.append('csv', file);
      if (name.trim()) form.append('name', name.trim());
      // Multipart, so this goes around the JSON helper — but it still carries
      // the CSRF header the server requires on every mutation.
      const res = await fetch(`${API_BASE}/trivia/questions/import`, {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'X-Kit-Web': '1' },
        body: form,
      });
      if (!res.ok) throw new Error((await res.text()).trim() || res.statusText);
      setReport((await res.json()) as ImportReport);
      setName('');
      if (fileRef.current) fileRef.current.value = '';
    });

  const total = datasets.reduce((n, d) => n + d.questions, 0);

  return (
    <>
      <div className="crumbs"><Link to="/admin">Admin</Link> / Trivia questions</div>
      <div className="page-head">
        <h1>Trivia questions</h1>
        <p className="page-sub">
          A set is a named group of questions. Games draw their board from whichever sets they
          pick, which is how a Christmas quiz stays separate from an ordinary Tuesday.
          {total ? ` ${total} question${total === 1 ? '' : 's'} across ${datasets.length} set${datasets.length === 1 ? '' : 's'}.` : ''}
        </p>
      </div>

      {err ? <p className="banner banner-error">{err}</p> : null}

      <section className="panel">
        <h2>Your sets</h2>
        {datasets.length === 0 ? (
          <p className="page-sub">Nothing yet. Add a set Kit ships, or upload a CSV.</p>
        ) : (
          <ul className="card-list">
            {datasets.map((d) => (
              <li key={d.id} className="card">
                <div className="card-main">
                  {renaming === d.id ? (
                    <input
                      className="field-inline"
                      value={draft}
                      autoFocus
                      onChange={(e) => setDraft(e.target.value)}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter') {
                          void run(async () => {
                            await api.renameTriviaDataset(d.id, draft.trim(), d.notes);
                            setRenaming(null);
                          });
                        }
                        if (e.key === 'Escape') setRenaming(null);
                      }}
                    />
                  ) : (
                    <span className="card-title">{d.name}</span>
                  )}
                  <span className="card-desc">
                    {d.questions} question{d.questions === 1 ? '' : 's'} · {d.topics} topic
                    {d.topics === 1 ? '' : 's'}
                    {d.builtin_key ? ' · shipped with Kit' : ''}
                    {d.notes ? ` · ${d.notes}` : ''}
                  </span>
                </div>
                <div className="card-side">
                  <button className="card-manage" disabled={busy}
                    onClick={() => { setRenaming(d.id); setDraft(d.name); }}>
                    Rename
                  </button>
                  {/* Two taps: deleting a set takes its questions with it. */}
                  {confirming === d.id ? (
                    <>
                      <button className="btn btn-danger" disabled={busy}
                        onClick={() => void run(async () => {
                          await api.deleteTriviaDataset(d.id);
                          setConfirming(null);
                        })}>
                        Really delete
                      </button>
                      <button className="btn btn-danger" disabled={busy}
                        onClick={() => setConfirming(null)}>Cancel</button>
                    </>
                  ) : (
                    <button className="btn btn-danger" disabled={busy}
                      onClick={() => setConfirming(d.id)}>Delete</button>
                  )}
                </div>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="panel">
        <h2>Sets Kit ships</h2>
        <ul className="card-list">
          {packs.map((p) => (
            <li key={p.key} className="card">
              <div className="card-main">
                <span className="card-title">{p.name}</span>
                <span className="card-desc">{p.notes}</span>
              </div>
              <div className="card-side">
                <button className="btn" disabled={busy}
                  onClick={() => void run(async () => { setReport(await api.loadTriviaPack(p.key)); })}>
                  {datasets.some((d) => d.builtin_key === p.key) ? 'Reload' : 'Add'}
                </button>
              </div>
            </li>
          ))}
        </ul>
        <p className="page-sub">
          Adding one creates an ordinary set — rename it, upload over it or delete it like any
          other. <a href={`${API_BASE}/trivia/questions/sample`}>Download a CSV template</a>.
        </p>
      </section>

      <section className="panel">
        <h2>Upload a set</h2>
        <div className="field-row">
          <label className="field">
            <span>Name</span>
            <input placeholder="or we'll use the file name" value={name} disabled={busy}
              onChange={(e) => setName(e.target.value)} />
          </label>
        </div>
        <input ref={fileRef} type="file" accept=".csv,text/csv" disabled={busy}
          onChange={(e) => {
            const f = e.target.files?.[0];
            if (f) void upload(f);
          }} />
        <p className="page-sub">
          Columns <code>question</code>, <code>topics</code> and <code>answer</code>, in any order.
          Topics are separated by semicolons. Answers must be numbers — the whole game is
          &ldquo;closest without going over&rdquo;. Uploading a set with a name that already exists
          replaces its contents.
        </p>
        {report ? (
          <div className="banner banner-ok">
            {report.imported} added, {report.updated} updated, {report.skipped_duplicates} duplicate
            {report.skipped_duplicates === 1 ? '' : 's'} skipped.
            {report.errors.length ? (
              <ul>
                {report.errors.map((e) => (
                  <li key={e.line}>Line {e.line}: {e.message}</li>
                ))}
                {report.truncated ? <li>…and more.</li> : null}
              </ul>
            ) : null}
          </div>
        ) : null}
      </section>
    </>
  );
}
