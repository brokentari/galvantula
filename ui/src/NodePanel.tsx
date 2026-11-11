import { GateReview } from "./GateReview";
import type { RunView } from "./state";

export function NodePanel({ run, id, live, onClose, onSelect }: { run: RunView; id: string; live: boolean; onClose: () => void; onSelect: (id: string) => void }) {
  const node = run.graph.nodes.find((n) => n.id === id);
  const view = run.nodes[id];
  if (!node || !view) return null;
  const gate = node.kind === "gate";

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
        {!gate && (
          <>
            <dt>attempt</dt>
            <dd>{view.attempt || "–"}</dd>
            <dt>cost</dt>
            <dd>${view.costUSD.toFixed(2)}</dd>
            <dt>retries</dt>
            <dd>
              {view.taskFailures} of {node.retries ?? 0} used
              {view.infraFailures > 0 && ` · ${view.infraFailures} infrastructure failures (not counted)`}
            </dd>
          </>
        )}
        {node.deps?.length ? (
          <>
            <dt>after</dt>
            <dd className="mono">
              {node.deps.map((d, i) => (
                <span key={d}>
                  {i > 0 && ", "}
                  <button className="link" onClick={() => onSelect(d)}>
                    {d}
                  </button>
                </span>
              ))}
            </dd>
          </>
        ) : null}
      </dl>
      {view.lastError &&
        (view.status === "failed" ? (
          <p className="error">{view.lastError}</p>
        ) : (
          <p className="muted">Last failure: {view.lastError}</p>
        ))}

      {gate ? (
        <GateReview run={run} id={id} live={live} onSelect={onSelect} />
      ) : (
        <>
          <h3>Log</h3>
          <pre className="log">{view.logs.length ? view.logs.join("\n") : "No output yet."}</pre>
        </>
      )}
    </section>
  );
}
