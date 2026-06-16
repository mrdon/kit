import type { Task } from '../../api';
import {
  PRIORITIES,
  PRIORITY_LABEL,
  STATUSES,
  STATUS_LABEL,
  fmtDate,
  priorityClass,
  statusClass,
} from './common';

interface Props {
  tasks: Task[];
  onQuickEdit: (id: string, patch: { status?: string; priority?: string }) => void;
  onOpen: (id: string) => void;
}

export default function TaskList({ tasks, onQuickEdit, onOpen }: Props) {
  if (tasks.length === 0) return <p className="muted">No tasks match these filters.</p>;
  return (
    <table className="task-table">
      <thead>
        <tr>
          <th>Title</th>
          <th>Status</th>
          <th>Priority</th>
          <th>Role</th>
          <th>Assignee</th>
          <th>Due</th>
        </tr>
      </thead>
      <tbody>
        {tasks.map((t) => (
          <tr key={t.id}>
            <td>
              <button className="link-btn" onClick={() => onOpen(t.id)}>
                {t.title}
              </button>
            </td>
            <td>
              <select
                className={`mini-select ${statusClass(t.status)}`}
                value={t.status}
                onChange={(e) => onQuickEdit(t.id, { status: e.target.value })}
              >
                {STATUSES.map((s) => (
                  <option key={s} value={s}>
                    {STATUS_LABEL[s]}
                  </option>
                ))}
              </select>
            </td>
            <td>
              <select
                className={`mini-select ${priorityClass(t.priority)}`}
                value={t.priority}
                onChange={(e) => onQuickEdit(t.id, { priority: e.target.value })}
              >
                {PRIORITIES.map((p) => (
                  <option key={p} value={p}>
                    {PRIORITY_LABEL[p]}
                  </option>
                ))}
              </select>
            </td>
            <td className="muted-cell">{t.role_name}</td>
            <td className="muted-cell">{t.assignee_name || '—'}</td>
            <td className="muted-cell">{fmtDate(t.due_date)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
