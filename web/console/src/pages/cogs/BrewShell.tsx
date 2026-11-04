import { useCallback, useState } from 'react';
import { Link, NavLink, Outlet } from 'react-router-dom';
import { api, type ActionInput, type BrewState } from '../../api';
import { useMe } from '../../me';
import { usePolled } from '../../usePolled';
import { BrewContext, type BrewCtx, type FormSpec } from './context';
import FormHost from './FormHost';

// BrewShell is the brewer's app: the bar with Tanks · Packaged · Schedule
// and Brew day, the banner that confirms what was just logged (with Undo),
// and the sheet any form opens in. It is the home of the brewhouse iPad
// and the same screens on the console for anyone else.

interface Toast {
  text: string;
  actionId?: string;
  error?: boolean;
}

export default function BrewShell() {
  const me = useMe();
  const isAdmin = me?.kind !== 'device' && !!me?.is_admin;
  const [state, setState] = useState<BrewState | null>(null);
  const [loadErr, setLoadErr] = useState<string | null>(null);
  const [form, setForm] = useState<FormSpec | null>(null);
  const [toast, setToast] = useState<Toast | null>(null);

  const reload = useCallback(
    () =>
      api
        .brewState()
        .then((s) => {
          setState(s);
          setLoadErr(null);
        })
        .catch((e) => setLoadErr((e as Error).message)),
    [],
  );
  usePolled(reload, 30000);

  const record = useCallback(
    async (input: ActionInput) => {
      try {
        const res = await api.brewRecord(input);
        setForm(null);
        setToast({ text: res.message, actionId: res.action_id });
        void reload();
        return null;
      } catch (e) {
        return (e as Error).message;
      }
    },
    [reload],
  );

  const undo = useCallback(
    async (actionId: string) => {
      try {
        const res = await api.brewUndo(actionId);
        setToast({ text: res.message });
      } catch (e) {
        setToast({ text: (e as Error).message, error: true });
      }
      void reload();
    },
    [reload],
  );

  if (!state) {
    return (
      <div className="brew">
        <div className="brew-body">{loadErr ? <p className="brew-error">{loadErr}</p> : <p>Loading…</p>}</div>
      </div>
    );
  }

  const ctx: BrewCtx = {
    state,
    isAdmin,
    open: setForm,
    close: () => setForm(null),
    record,
    undo,
    reload: () => void reload(),
    notify: (text) => setToast({ text }),
  };
  const today = new Date(`${state.today}T12:00:00`).toLocaleDateString(undefined, {
    weekday: 'short',
    month: 'short',
    day: 'numeric',
  });

  return (
    <BrewContext.Provider value={ctx}>
      <div className="brew">
        <header className="brew-bar">
          <span className="brew-bar-title">
            Brewhouse<span className="brew-bar-date">{today}</span>
          </span>
          <nav className="brew-tabs" aria-label="Brewing screens">
            <NavLink end to="/cogs" className={({ isActive }) => `brew-tab ${isActive ? 'brew-tab-active' : ''}`}>
              Tanks
            </NavLink>
            <NavLink to="/cogs/packaged" className={({ isActive }) => `brew-tab ${isActive ? 'brew-tab-active' : ''}`}>
              Packaged
            </NavLink>
            <NavLink to="/cogs/schedule" className={({ isActive }) => `brew-tab ${isActive ? 'brew-tab-active' : ''}`}>
              Schedule
            </NavLink>
          </nav>
          <button type="button" className="brew-brewday" onClick={() => setForm({ kind: 'brew' })}>
            Brew day
          </button>
        </header>
        {isAdmin && (
          <nav className="brew-admin-links" aria-label="Brewing console">
            <Link to="/cogs/setup">Setup</Link>
            <Link to="/cogs/load">Day-one load</Link>
            <Link to="/cogs/plan">Plan</Link>
            <Link to="/cogs/queue">Queue</Link>
            <Link to="/cogs/history">Corrections</Link>
          </nav>
        )}
        {toast && (
          <div role="status" className={`brew-toast ${toast.error ? 'brew-toast-error' : ''}`}>
            <span className="brew-toast-text">{toast.text}</span>
            {toast.actionId && (
              <button
                type="button"
                className="brew-toast-undo"
                onClick={() => {
                  const id = toast.actionId!;
                  setToast(null);
                  void undo(id);
                }}
              >
                Undo
              </button>
            )}
            <button type="button" onClick={() => setToast(null)}>
              OK
            </button>
          </div>
        )}
        {loadErr && <p className="brew-error" style={{ margin: '0.75rem 1.5rem 0' }}>{loadErr}</p>}
        <div className="brew-body">
          <Outlet />
        </div>
        {form && <FormHost spec={form} />}
      </div>
    </BrewContext.Provider>
  );
}
