import { useState } from 'react';
import type { Task } from '../../api';
import {
  NO_CATEGORY,
  PRIORITIES,
  PRIORITY_LABEL,
  fmtDate,
  priorityClass,
} from './common';

interface Props {
  tasks: Task[];
  meId: string;
  categories: string[];
  onReprioritize: (id: string, priority: string) => void;
  onClaim: (id: string, claim: boolean) => void;
  onResolve: (id: string) => void;
  onSetCategory: (id: string, category: string) => void;
  onOpen: (id: string) => void;
}

// "Claimed" keys off assignee, not status: assigning a task reserves it, so
// teammates can block work off for each other before anyone starts. An
// open-but-assigned task is just as taken as an in-progress one.
const claimedByOther = (t: Task, meId: string) =>
  !!t.assignee_user_id && t.assignee_user_id !== meId;

const claimedByMe = (t: Task, meId: string) => t.assignee_user_id === meId;

// Sort within a category: others' claims last, then due date (soonest first,
// undated last), then newest.
function rowSort(meId: string) {
  return (a: Task, b: Task) => {
    const ao = claimedByOther(a, meId) ? 1 : 0;
    const bo = claimedByOther(b, meId) ? 1 : 0;
    if (ao !== bo) return ao - bo;
    const ad = a.due_date ?? '9999-99';
    const bd = b.due_date ?? '9999-99';
    if (ad !== bd) return ad < bd ? -1 : 1;
    return a.created_at < b.created_at ? 1 : -1;
  };
}

// Group a band's tasks by category, Uncategorized last, otherwise A→Z.
function byCategory(tasks: Task[]): [string, Task[]][] {
  const m = new Map<string, Task[]>();
  for (const t of tasks) {
    const k = t.category?.trim() || NO_CATEGORY;
    (m.get(k) ?? m.set(k, []).get(k)!).push(t);
  }
  return [...m.entries()].sort(([a], [b]) => {
    if (a === NO_CATEGORY) return 1;
    if (b === NO_CATEGORY) return -1;
    return a.localeCompare(b);
  });
}

export default function TaskGrouped({
  tasks,
  meId,
  categories,
  onReprioritize,
  onClaim,
  onResolve,
  onSetCategory,
  onOpen,
}: Props) {
  const [dragId, setDragId] = useState<string | null>(null);
  const [overBand, setOverBand] = useState<string | null>(null);

  if (tasks.length === 0) {
    return <p className="muted">No tasks match these filters.</p>;
  }

  const drop = (priority: string) => {
    if (dragId) onReprioritize(dragId, priority);
    setDragId(null);
    setOverBand(null);
  };

  return (
    <div className="bands">
      {PRIORITIES.map((band) => {
        const inBand = tasks.filter((t) => t.priority === band);
        return (
          <section
            key={band}
            className={`band${overBand === band ? ' band-over' : ''}`}
            onDragOver={(e) => {
              e.preventDefault();
              setOverBand(band);
            }}
            onDragLeave={() => setOverBand((b) => (b === band ? null : b))}
            onDrop={() => drop(band)}
          >
            <header className="band-head">
              <span className={`band-dot ${priorityClass(band)}`} />
              <h3>{PRIORITY_LABEL[band]}</h3>
              <span className="band-count">{inBand.length}</span>
            </header>

            {inBand.length === 0 ? (
              <p className="band-empty">Drag a task here to mark it {PRIORITY_LABEL[band].toLowerCase()}.</p>
            ) : (
              byCategory(inBand).map(([cat, rows]) => (
                <div key={cat} className="cat-group">
                  <div className="cat-head">{cat}</div>
                  {rows.sort(rowSort(meId)).map((t) => (
                    <TaskRow
                      key={t.id}
                      t={t}
                      meId={meId}
                      categories={categories}
                      onDragStart={() => setDragId(t.id)}
                      onDragEnd={() => setDragId(null)}
                      onClaim={onClaim}
                      onResolve={onResolve}
                      onSetCategory={onSetCategory}
                      onOpen={onOpen}
                    />
                  ))}
                </div>
              ))
            )}
          </section>
        );
      })}
    </div>
  );
}

interface RowProps {
  t: Task;
  meId: string;
  categories: string[];
  onDragStart: () => void;
  onDragEnd: () => void;
  onClaim: (id: string, claim: boolean) => void;
  onResolve: (id: string) => void;
  onSetCategory: (id: string, category: string) => void;
  onOpen: (id: string) => void;
}

function TaskRow({
  t,
  meId,
  categories,
  onDragStart,
  onDragEnd,
  onClaim,
  onResolve,
  onSetCategory,
  onOpen,
}: RowProps) {
  const mine = claimedByMe(t, meId);
  const other = claimedByOther(t, meId);
  const catOptions = [...new Set([...categories, t.category].filter(Boolean) as string[])];

  return (
    <article
      className={`trow${mine ? ' trow-mine' : ''}${other ? ' trow-other' : ''}`}
      draggable
      onDragStart={onDragStart}
      onDragEnd={onDragEnd}
    >
      <button
        className="trow-check"
        title="Resolve"
        aria-label="Resolve task"
        onClick={() => onResolve(t.id)}
      >
        ✓
      </button>

      <button className="trow-title link-btn" onClick={() => onOpen(t.id)}>
        {t.title}
      </button>

      <div className="trow-meta">
        {catOptions.length > 0 && (
          <select
            className="mini-select cat-select"
            value={t.category ?? ''}
            title="Category"
            onClick={(e) => e.stopPropagation()}
            onChange={(e) => onSetCategory(t.id, e.target.value)}
          >
            {!t.category && <option value="">—</option>}
            {catOptions.map((c) => (
              <option key={c} value={c}>
                {c}
              </option>
            ))}
          </select>
        )}
        {t.due_date && <span className="tag tag-soft">{fmtDate(t.due_date)}</span>}
        {other ? (
          <span className="tag tag-onit" title="Someone is on this — skip it">
            ● {t.assignee_name || 'on it'}
          </span>
        ) : (
          <button
            className={`tag tag-claim${mine ? ' tag-claim-on' : ''}`}
            onClick={() => onClaim(t.id, !mine)}
          >
            {mine ? "I'm on it ✓" : "I'm on it"}
          </button>
        )}
      </div>
    </article>
  );
}
