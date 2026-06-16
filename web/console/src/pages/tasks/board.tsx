import { useState } from 'react';
import type { Task } from '../../api';
import {
  BOARD_STATUSES,
  PRIORITY_LABEL,
  STATUS_LABEL,
  fmtDate,
  priorityClass,
} from './common';

export type Swimlane = 'none' | 'role' | 'assignee';

interface Props {
  tasks: Task[];
  swimlane: Swimlane;
  onMove: (taskId: string, status: string) => void;
  onOpen: (taskId: string) => void;
}

function laneKey(t: Task, swim: Swimlane): string {
  if (swim === 'role') return t.role_name || 'No role';
  if (swim === 'assignee') return t.assignee_name || 'Unassigned';
  return '';
}

export default function TaskBoard({ tasks, swimlane, onMove, onOpen }: Props) {
  const [dragId, setDragId] = useState<string | null>(null);
  const [overCol, setOverCol] = useState<string | null>(null);

  // Group into swimlanes (a single '' lane when swimlane === 'none').
  const lanes = new Map<string, Task[]>();
  for (const t of tasks) {
    const k = laneKey(t, swimlane);
    (lanes.get(k) ?? lanes.set(k, []).get(k)!).push(t);
  }
  const laneKeys = [...lanes.keys()].sort();

  const drop = (status: string) => {
    if (dragId) onMove(dragId, status);
    setDragId(null);
    setOverCol(null);
  };

  return (
    <div className="board-wrap">
      {laneKeys.map((lk) => (
        <div key={lk} className="swimlane">
          {swimlane !== 'none' && <h3 className="swimlane-head">{lk}</h3>}
          <div className="board">
            {BOARD_STATUSES.map((status) => {
              const colTasks = lanes.get(lk)!.filter((t) => t.status === status);
              const colKey = `${lk}::${status}`;
              return (
                <div
                  key={status}
                  className={`board-col${overCol === colKey ? ' board-col-over' : ''}`}
                  onDragOver={(e) => {
                    e.preventDefault();
                    setOverCol(colKey);
                  }}
                  onDragLeave={() => setOverCol((c) => (c === colKey ? null : c))}
                  onDrop={() => drop(status)}
                >
                  <div className="board-col-head">
                    <span>{STATUS_LABEL[status]}</span>
                    <span className="board-count">{colTasks.length}</span>
                  </div>
                  {colTasks.map((t) => (
                    <article
                      key={t.id}
                      className="board-card"
                      draggable
                      onDragStart={() => setDragId(t.id)}
                      onDragEnd={() => setDragId(null)}
                      onClick={() => onOpen(t.id)}
                    >
                      <span className="board-card-title">{t.title}</span>
                      <div className="board-card-meta">
                        <span className={`tag ${priorityClass(t.priority)}`}>
                          {PRIORITY_LABEL[t.priority]}
                        </span>
                        {swimlane !== 'assignee' && t.assignee_name && (
                          <span className="tag tag-soft">{t.assignee_name}</span>
                        )}
                        {t.due_date && (
                          <span className="tag tag-soft">{fmtDate(t.due_date)}</span>
                        )}
                      </div>
                    </article>
                  ))}
                </div>
              );
            })}
          </div>
        </div>
      ))}
    </div>
  );
}
