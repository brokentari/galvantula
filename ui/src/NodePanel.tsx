import { useState } from "react";
import type { DecideRequest } from "./gen/events";
import type { RunView } from "./state";

export function NodePanel({ run, id, live, onClose }: { run: RunView; id: string; live: boolean; onClose: () => void }) {
  const node = run.graph.nodes.find((n) => n.id === id);
  const view = run.nodes[id];
  const [notes, setNotes] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  if (!node || !view) return null;

  async function decide(approved: boolean) {
    setBusy(true);
    setError("");
    const body: DecideRequest = { approved, notes };
    try {
      const res = await fetch(`/api/runs/${run.id}/gates/${id}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (!res.ok) setError((await res.text()) || res.statusText);
      else setNotes("");
    } catch (err) {
      setError(String(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="panel-section">
      <div className="panel-head">
        <h2 className="mono">{node.id}</h2>
        <button className="link" onClick={onClose}>
          back to run
        </button>
      </div>
      {node.title && <p className="muted">{node.title}</p>}
      <dl className="facts">
        <dt>kind</dt>
        <dd>{node.kind}</dd>
        <dt>status</dt>
        <dd className={`status-text status-${view.status}`}>{view.status}</dd>
        <dt>attempt</dt>
        <dd>{view.attempt || "–"}</dd>
        <dt>retries</dt>
        <dd>
          {view.taskFailures} of {node.retries ?? 0} used
          {view.infraFailures > 0 && ` · ${view.infraFailures} infrastructure failures (not counted)`}
        </dd>
        {node.deps?.length ? (
          <>
            <dt>after</dt>
            <dd className="mono">{node.deps.join(", ")}</dd>
          </>
        ) : null}
      </dl>
      {view.lastError &&
        (view.status === "failed" ? (
          <p className="error">{view.lastError}</p>
        ) : (
          <p className="muted">Last failure: {view.lastError}</p>
        ))}

      {node.kind === "gate" && view.status === "waiting" && (
        <div className="gate">
          <h3>This gate waits for you</h3>
          {!live && <p className="muted">Go back to live to decide.</p>}
          <textarea
            placeholder="Notes (sent back with a rejection)"
            value={notes}
            onChange={(e) => setNotes(e.target.value)}
            rows={3}
          />
          <div className="gate-buttons">
            <button className="approve" disabled={busy || !live} onClick={() => decide(true)}>
              Approve
            </button>
            <button className="reject" disabled={busy || !live} onClick={() => decide(false)}>
              Reject
            </button>
          </div>
          {error && <p className="error">{error}</p>}
        </div>
      )}

      <h3>Log</h3>
      <pre className="log">{view.logs.length ? view.logs.join("\n") : "No output yet."}</pre>
    </section>
  );
}
