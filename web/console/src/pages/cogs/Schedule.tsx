import { useSetChatContext } from '../../chatContext';
import { useBrew } from './context';

// Schedule: four weeks of lanes for the fermenters and the brite tank.
// Free time is drawn on purpose: "FV-7 is free all month" is a planning
// signal. Read-only here; tapping a planned brew says why it's planned and
// lets the brewer say the date won't work.

const DAYS = 28;

export default function Schedule() {
  useSetChatContext('the brewing schedule page');
  const { state, open } = useBrew();
  const base = new Date(`${state.today}T12:00:00`);
  const labels = Array.from({ length: DAYS }, (_, i) => {
    const d = new Date(base);
    d.setDate(base.getDate() + i);
    return { i, text: d.getDay() === 1 || i === 0 ? `${d.getMonth() + 1}/${d.getDate()}` : '' };
  });
  if (state.schedule.length === 0) return <p className="brew-empty">No fermenters are set up yet.</p>;
  return (
    <div className="brew-sched">
      <div className="brew-sched-grid">
        <div className="brew-sched-days">
          <span />
          {labels.map((l) => (
            <span key={l.i}>{l.text}</span>
          ))}
        </div>
        {state.schedule.map((lane) => (
          <div key={lane.vessel_id} className="brew-lane">
            <span className="brew-lane-name">{lane.name}</span>
            {lane.bars.map((b, i) => {
              const style = { gridColumn: `${b.start + 2} / ${b.end + 2}` };
              const cls = `brew-bar-seg brew-seg-${b.kind} ${b.clash ? 'brew-seg-clash' : ''}`;
              if (b.kind === 'planned' && b.plan_entry_id) {
                const id = b.plan_entry_id;
                return (
                  <button key={i} type="button" className={cls} style={style} title={b.why || b.label}
                    onClick={() => open({ kind: 'plan', planEntryId: id })}>
                    {b.label}
                    {b.clash ? ' (clash)' : ''}
                  </button>
                );
              }
              return (
                <span key={i} className={cls} style={style} title={b.label}>
                  {b.kind === 'free' && b.end - b.start < 3 ? '' : b.label}
                </span>
              );
            })}
          </div>
        ))}
      </div>
      <p className="brew-hint" style={{ marginTop: '0.75rem' }}>
        Dashed bars are planned brews. Plans are set on the console; tap one to say a date won't work.
      </p>
    </div>
  );
}
