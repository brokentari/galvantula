import type { TaskRow } from "./gen/events";
import "./contract.css";

export function TaskTable({ tasks }: { tasks: TaskRow[] }) {
  return (
    <table className="task-table">
      <thead>
        <tr>
          {["Task", "Owns", "Depends on", "Retries", "Cost cap"].map((h) => (
            <th key={h} scope="col">{h}</th>
          ))}
        </tr>
      </thead>
      <tbody>
        {tasks.map((t) => (
          <tr key={t.id}>
            <td>{t.id}{t.title && <div className="muted">{t.title}</div>}</td>
            <td>{t.owns.join(", ")}</td>
            <td>{t.after.length ? t.after.join(", ") : "starts at once"}</td>
            <td>{t.retries}</td>
            <td>{t.cap_usd === 0 ? "no limit" : `$${t.cap_usd.toFixed(2)}`}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
