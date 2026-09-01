import { useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  api,
  type ChannelMode,
  type ChannelsPayload,
  type EventChannel,
  type ChannelStep,
  type Prominence,
  type StepKind,
} from '../api';
import { useSetChatContext } from '../chatContext';

// Admin page for promotion channels: where events get promoted, and what Kit
// does about each one.
//
// The thing this page is really for is moving channels LEFTWARD — manual to
// subscribed, or manual to automated. Every channel that stops needing a human
// retires a recurring chore rather than making it faster, so the copyable feed
// URLs sit right here next to the mode picker.
//
// Steps are never hand-authored as JSON. Presets write the array; the grid
// below edits which prominence levels each step runs at.

const PROMINENCES: Prominence[] = ['background', 'normal', 'featured'];

// Presets exist so nobody types a step array. Each is the shape a real
// destination actually wants.
const PRESETS: { id: string; label: string; blurb: string; steps: ChannelStep[] }[] = [
  {
    id: 'submit-once',
    label: 'Submit once',
    blurb: 'A community calendar you fill a form in for. Uses the lead time.',
    steps: [{ key: 'submit', label: 'Submit to calendar', kind: 'oneshot' }],
  },
  {
    id: 'announce-remind',
    label: 'Announce + remind',
    blurb: 'A feed post three weeks out, a nudge a week out, a day-before.',
    steps: [
      { key: 'announce', label: 'Announce', kind: 'drip', offset_days: 21, automatable: true },
      { key: 'remind', label: 'Remind', kind: 'drip', offset_days: 7, expires_after_days: 4, min_prominence: 'featured', automatable: true },
      { key: 'day-before', label: 'Day before', kind: 'drip', offset_days: 1, expires_after_days: 1, automatable: true },
    ],
  },
  {
    id: 'day-of',
    label: 'Day-of only',
    blurb: 'Stories. They vanish in 24 hours, so an early post is wasted.',
    steps: [
      { key: 'day-before', label: 'Day before', kind: 'drip', offset_days: 1, expires_after_days: 1, automatable: true },
      { key: 'day-of', label: 'Day of', kind: 'drip', offset_days: 0, expires_after_days: 1, automatable: true },
    ],
  },
  {
    id: 'cadence',
    label: 'Every few weeks',
    blurb: 'For standing series — remind people trivia exists, monthly-ish.',
    steps: [{ key: 'mention', label: 'Post about it', kind: 'cadence', interval_days: 28, automatable: true }],
  },
  {
    id: 'custom',
    label: 'Custom',
    blurb: 'Start empty and add steps by hand.',
    steps: [],
  },
];

function describeStep(s: ChannelStep): string {
  switch (s.kind) {
    case 'drip':
      return s.offset_days === 0 ? 'day of' : `${s.offset_days} days before`;
    case 'cadence':
      return `every ${s.interval_days ?? 28} days`;
    default:
      return 'once per event';
  }
}

const blank: Partial<EventChannel> = {
  name: '',
  mode: 'manual',
  submit_url: '',
  lead_time_days: 0,
  include_offsite: false,
  min_prominence: 'normal',
  active: true,
  steps: [],
};

export default function EventsChannelsPage() {
  useSetChatContext('the admin Event promotion channels page');
  const [data, setData] = useState<ChannelsPayload | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [draft, setDraft] = useState<Partial<EventChannel> | null>(null);

  const load = () => {
    api
      .listEventChannels()
      .then(setData)
      .catch((e) => setErr((e as Error).message));
  };
  useEffect(load, []);

  const run = async (fn: () => Promise<void>) => {
    setBusy(true);
    setErr(null);
    setNote(null);
    try {
      await fn();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const save = (c: Partial<EventChannel>) =>
    run(async () => {
      setData(await api.saveEventChannel(c));
      setDraft(null);
      setNote('Saved.');
    });

  const remove = (c: EventChannel) =>
    run(async () => {
      setData(await api.deleteEventChannel(c.id));
      setNote(`Removed ${c.name}.`);
    });

  const feedFor = (c: EventChannel) => {
    if (!data) return '';
    return c.feed_tier ? data.feed_urls[c.feed_tier] : '';
  };

  const channels = data?.channels ?? [];
  const noSite = useMemo(
    () => !!data && !data.feed_urls.all,
    [data],
  );

  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <Link to="/admin">Admin</Link>
          <span className="crumb-sep">/</span>
          <span>Event promotion</span>
        </nav>
        <h1>Event promotion</h1>
        <p className="page-sub">
          Where events get promoted, and what Kit does about each one. The goal
          is to have as few of these on <strong>Do it yourself</strong> as
          possible — a calendar that subscribes to your feed never needs
          another submission.
        </p>
      </div>

      {note && (
        <p className="banner banner-ok" onClick={() => setNote(null)}>
          {note}
        </p>
      )}
      {err && <p className="banner banner-error">{err}</p>}
      {!data && !err && <p className="muted">Loading…</p>}

      {data && (
        <>
          <section className="panel">
            <h2 className="panel-title">Your calendar feeds</h2>
            {noSite ? (
              <p className="muted">
                Set the website URL template on the{' '}
                <Link to="/admin/events">Events calendar &amp; feed</Link> page
                and the subscribable addresses will appear here.
              </p>
            ) : (
              <>
                <p className="field-note">
                  Send one of these to a calendar that will subscribe. Ask them
                  to <strong>subscribe</strong>, not import — a one-time import
                  silently goes stale and keeps re-listing cancelled events.
                </p>
                <table className="item-table">
                  <tbody>
                    <tr>
                      <td>Everything</td>
                      <td>
                        <code>{data.feed_urls.all}</code>
                      </td>
                      <td className="muted">
                        Regulars. Includes standing offers like happy hour.
                      </td>
                    </tr>
                    <tr>
                      <td>Highlights</td>
                      <td>
                        <code>{data.feed_urls.highlights}</code>
                      </td>
                      <td className="muted">
                        Real happenings, no standing offers.
                      </td>
                    </tr>
                    <tr>
                      <td>Featured only</td>
                      <td>
                        <code>{data.feed_urls.featured}</code>
                      </td>
                      <td className="muted">
                        Chambers and town calendars — the big ones.
                      </td>
                    </tr>
                  </tbody>
                </table>
              </>
            )}
          </section>

          <section className="panel">
            <h2 className="panel-title">Channels</h2>
            {channels.length === 0 && (
              <p className="muted">
                No channels yet. Add the places you post events — your chamber,
                the city calendar, Facebook, Instagram.
              </p>
            )}
            {channels.map((c) => (
              <ChannelCard
                key={c.id}
                channel={c}
                busy={busy}
                feedURL={feedFor(c)}
                onSave={save}
                onDelete={() => remove(c)}
              />
            ))}
          </section>

          <section className="panel">
            <h2 className="panel-title">Add a channel</h2>
            {draft ? (
              <ChannelForm
                value={draft}
                busy={busy}
                onChange={setDraft}
                onSave={() => save(draft)}
                onCancel={() => setDraft(null)}
              />
            ) : (
              <button className="btn" onClick={() => setDraft({ ...blank })}>
                New channel
              </button>
            )}
          </section>
        </>
      )}
    </div>
  );
}

function ChannelCard({
  channel,
  busy,
  feedURL,
  onSave,
  onDelete,
}: {
  channel: EventChannel;
  busy: boolean;
  feedURL: string;
  onSave: (c: Partial<EventChannel>) => void;
  onDelete: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState<Partial<EventChannel>>(channel);

  useEffect(() => setDraft(channel), [channel]);

  // A subscribed channel that nobody has confirmed is the one silent failure
  // mode in the whole system: it generates no work, so if they never finished
  // wiring up the feed, events stop reaching them and nothing says so.
  const unverified = channel.mode === 'subscribed' && !channel.verified_at;
  const stale =
    channel.mode === 'subscribed' &&
    !!channel.verified_at &&
    Date.now() - new Date(channel.verified_at).getTime() > 1000 * 60 * 60 * 24 * 180;

  return (
    <div className="item-row">
      <div className="page-head-row">
        <strong>{channel.name}</strong>{' '}
        <span className={channel.mode === 'manual' ? 'pill pill-off' : 'pill pill-ok'}>
          {channel.mode === 'manual'
            ? 'you do it'
            : channel.mode === 'subscribed'
              ? 'they pull the feed'
              : 'Kit posts it'}
        </span>
        {!channel.active && <span className="pill pill-off">off</span>}
        <button className="btn btn-ghost" onClick={() => setOpen(!open)}>
          {open ? 'Close' : 'Edit'}
        </button>
      </div>

      {channel.mode === 'subscribed' && (
        <p className="field-note">
          {unverified ? (
            <>
              ⚠ Nobody has confirmed they are actually pulling this yet. Until
              someone does, this channel produces no reminders — so if the
              subscription was never finished, your events quietly stop
              reaching them.
            </>
          ) : (
            <>
              Confirmed pulling since{' '}
              {new Date(channel.verified_at as string).toLocaleDateString()}
              {stale && ' — worth a spot-check.'}
              {feedURL && (
                <>
                  {' '}
                  (<code>{feedURL}</code>)
                </>
              )}
            </>
          )}
        </p>
      )}

      {open && (
        <ChannelForm
          value={draft}
          busy={busy}
          onChange={setDraft}
          onSave={() => onSave(draft)}
          onCancel={() => setOpen(false)}
          onDelete={onDelete}
        />
      )}
    </div>
  );
}

function ChannelForm({
  value,
  busy,
  onChange,
  onSave,
  onCancel,
  onDelete,
}: {
  value: Partial<EventChannel>;
  busy: boolean;
  onChange: (c: Partial<EventChannel>) => void;
  onSave: () => void;
  onCancel: () => void;
  onDelete?: () => void;
}) {
  const set = (patch: Partial<EventChannel>) => onChange({ ...value, ...patch });
  const steps = value.steps ?? [];

  // `automated` is not freely choosable: without a connector it would post
  // nothing while also silencing the checklist, which is worse than never
  // automating. Disabled until one exists.
  const canAutomate = !!value.connector;

  const setStep = (i: number, patch: Partial<ChannelStep>) =>
    set({ steps: steps.map((s, j) => (j === i ? { ...s, ...patch } : s)) });

  // The grid cell. An unset min_prominence inherits the channel's floor, so
  // ticking a lower level writes the step's own floor and unticking clears it
  // back to inheriting.
  const runsAt = (s: ChannelStep, p: Prominence) => {
    const floor = s.min_prominence || value.min_prominence || 'normal';
    return PROMINENCES.indexOf(p) >= PROMINENCES.indexOf(floor);
  };

  return (
    <div className="preview-body">
      <label className="field">
        <span>Name</span>
        <input
          value={value.name ?? ''}
          disabled={busy}
          onChange={(e) => set({ name: e.target.value })}
          placeholder="Louisville Chamber of Commerce"
        />
      </label>

      <label className="field">
        <span>How does it get there?</span>
        <select
          value={value.mode}
          disabled={busy}
          onChange={(e) => set({ mode: e.target.value as ChannelMode })}
        >
          <option value="manual">You do it — Kit reminds you</option>
          <option value="subscribed">They subscribe to your feed — nothing to do</option>
          <option value="automated" disabled={!canAutomate}>
            Kit posts it{canAutomate ? '' : ' — needs a connector'}
          </option>
        </select>
      </label>

      {value.mode === 'subscribed' && (
        <>
          <label className="field">
            <span>Which feed do they pull?</span>
            <select
              value={value.feed_tier ?? 'featured'}
              disabled={busy}
              onChange={(e) =>
                set({ feed_tier: e.target.value as EventChannel['feed_tier'] })
              }
            >
              <option value="featured">Featured only</option>
              <option value="highlights">Highlights</option>
              <option value="all">Everything</option>
            </select>
          </label>
          <label className="field">
            <span>
              <input
                type="checkbox"
                checked={!!value.verified_at}
                disabled={busy}
                onChange={(e) =>
                  set({ verified_at: e.target.checked ? new Date().toISOString() : undefined })
                }
              />{' '}
              I have confirmed they are pulling it
            </span>
            <span className="field-note">
              Leave this unticked until you have actually seen an event of
              yours appear on their calendar.
            </span>
          </label>
        </>
      )}

      {value.mode !== 'subscribed' && (
        <>
          <label className="field">
            <span>Submit page</span>
            <input
              value={value.submit_url ?? ''}
              disabled={busy}
              onChange={(e) => set({ submit_url: e.target.value })}
              placeholder="https://chamber.example/submit-an-event"
            />
            <span className="field-note">
              Opened straight from the checklist, so make it as deep a link as
              you can — the actual submit form, not their home page.
            </span>
          </label>

          <label className="field">
            <span>Notice needed (days)</span>
            <input
              type="number"
              min={0}
              value={value.lead_time_days ?? 0}
              disabled={busy}
              onChange={(e) => set({ lead_time_days: Number(e.target.value) })}
            />
            <span className="field-note">
              How far ahead they want telling. This is what sets urgency — an
              event three weeks out is already late for a calendar that wants a
              month.
            </span>
          </label>

          <label className="field">
            <span>
              <input
                type="checkbox"
                checked={!!value.include_offsite}
                disabled={busy}
                onChange={(e) => set({ include_offsite: e.target.checked })}
              />{' '}
              Include events we are only attending
            </span>
            <span className="field-note">
              Festivals elsewhere. Usually off for a community calendar &mdash;
              they already list it from whoever is running it &mdash; and on
              for your own social accounts, where &ldquo;come see us
              there&rdquo; is the whole point.
            </span>
          </label>

          <label className="field">
            <span>Start from a preset</span>
            <select
              value=""
              disabled={busy}
              onChange={(e) => {
                const p = PRESETS.find((x) => x.id === e.target.value);
                if (p) set({ steps: p.steps.map((s) => ({ ...s })) });
              }}
            >
              <option value="">Choose…</option>
              {PRESETS.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.label} — {p.blurb}
                </option>
              ))}
            </select>
          </label>

          {steps.length > 0 && (
            <table className="item-table">
              <thead>
                <tr>
                  <th>Step</th>
                  <th>When</th>
                  {PROMINENCES.map((p) => (
                    <th key={p}>{p}</th>
                  ))}
                  <th />
                </tr>
              </thead>
              <tbody>
                {steps.map((s, i) => (
                  <tr key={s.key}>
                    <td>{s.label || s.key}</td>
                    <td className="muted">{describeStep(s)}</td>
                    {PROMINENCES.map((p) => (
                      <td key={p}>
                        <input
                          type="checkbox"
                          checked={runsAt(s, p)}
                          disabled={busy}
                          onChange={(e) =>
                            setStep(i, {
                              min_prominence: e.target.checked
                                ? p
                                : (PROMINENCES[PROMINENCES.indexOf(p) + 1] ?? 'featured'),
                            })
                          }
                        />
                      </td>
                    ))}
                    <td>
                      {!s.automatable && value.mode === 'automated' && (
                        <span className="pill pill-off">manual</span>
                      )}
                      <button
                        className="btn btn-ghost"
                        disabled={busy}
                        onClick={() => set({ steps: steps.filter((_, j) => j !== i) })}
                      >
                        Remove
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          <p className="field-note">
            A tick means that step runs for events at that level. Recurring
            series only ever get the once-per-event and every-few-weeks steps —
            you do not run an announce campaign for something that happens
            every Tuesday.
          </p>
        </>
      )}

      <label className="field">
        <span>
          <input
            type="checkbox"
            checked={value.active ?? true}
            disabled={busy}
            onChange={(e) => set({ active: e.target.checked })}
          />{' '}
          Active
        </span>
      </label>

      <div className="page-head-actions">
        <button className="btn" disabled={busy} onClick={onSave}>
          Save
        </button>
        <button className="btn btn-ghost" disabled={busy} onClick={onCancel}>
          Cancel
        </button>
        {onDelete && (
          <button className="btn btn-ghost" disabled={busy} onClick={onDelete}>
            Delete
          </button>
        )}
      </div>
    </div>
  );
}

export type { StepKind };
