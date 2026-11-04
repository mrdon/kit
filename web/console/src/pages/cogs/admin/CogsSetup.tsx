import { useEffect, useState } from 'react';
import { api, type BrewSettings, type BrewSetup } from '../../../api';
import { useSetChatContext } from '../../../chatContext';
import { Crumbs, num } from './common';
import SetupVessels from './SetupVessels';
import SetupFormats from './SetupFormats';
import SetupBeers from './SetupBeers';

// The brewery as config: its vessels, package formats, beers and a few
// globals. What is in each vessel is never edited here; it comes from the
// log (and the day-one load).
export default function CogsSetup() {
  useSetChatContext('the brewing setup page');
  const [setup, setSetup] = useState<BrewSetup | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const load = () =>
    api
      .brewSetup()
      .then(setSetup)
      .catch((e) => setErr((e as Error).message));
  useEffect(() => {
    void load();
  }, []);

  return (
    <div className="page brew-admin">
      <div className="page-head">
        <Crumbs page="Setup" />
        <h1>Brewery setup</h1>
        <p className="page-sub">
          Vessels, package formats and beers. What is in each tank comes from the brewer's log, not from here.
        </p>
      </div>
      {err && <p className="banner banner-error">{err}</p>}
      {setup && (
        <>
          <SetupVessels vessels={setup.vessels} reload={load} onError={setErr} />
          <SetupFormats formats={setup.formats} reload={load} onError={setErr} />
          <SetupBeers beers={setup.beers} reload={load} onError={setErr} />
          <SettingsPanel settings={setup.settings} reload={load} onError={setErr} />
        </>
      )}
    </div>
  );
}

function SettingsPanel({
  settings,
  reload,
  onError,
}: {
  settings: BrewSettings;
  reload: () => void;
  onError: (m: string | null) => void;
}) {
  const [d, setD] = useState({
    partial_fullness: String(settings.partial_fullness),
    barrel_bbl: String(settings.barrel_bbl),
    brewhouse_bbl: String(settings.brewhouse_bbl),
    count_interval_days: String(settings.count_interval_days),
  });
  const [saved, setSaved] = useState(false);

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    onError(null);
    try {
      await api.brewSaveSettings({
        partial_fullness: num(d.partial_fullness),
        barrel_bbl: num(d.barrel_bbl),
        brewhouse_bbl: num(d.brewhouse_bbl),
        count_interval_days: Math.round(num(d.count_interval_days)),
      });
      setSaved(true);
      setTimeout(() => setSaved(false), 1500);
      reload();
    } catch (e) {
      onError((e as Error).message);
    }
  };

  const field = (key: keyof typeof d, label: string, note: string) => (
    <label className="field">
      {label}
      <input inputMode="decimal" value={d[key]} onChange={(e) => setD({ ...d, [key]: e.target.value })} />
      <span className="field-note">{note}</span>
    </label>
  );

  return (
    <section className="panel">
      <h2 className="panel-title">Settings</h2>
      <form className="stack-form" onSubmit={save}>
        <div className="field-row">
          {field('brewhouse_bbl', 'Brewhouse size (bbl)', 'What brew day knocks out by default.')}
          {field('barrel_bbl', 'Barrel size (bbl)', 'What a barrel fill takes from the tank.')}
        </div>
        <div className="field-row">
          {field('partial_fullness', 'A partial keg is this full', 'Between 0 and 1. 0.5 is half full.')}
          {field('count_interval_days', 'Count every (days)', 'When the packaged count shows as due.')}
        </div>
        <div>
          <button className="btn" type="submit">
            {saved ? 'Saved' : 'Save settings'}
          </button>
        </div>
      </form>
    </section>
  );
}
