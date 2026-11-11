import type { RunView } from "./state";
import { clock, useNow } from "./time";

// RunStats shows the elapsed time, nodes done and cost of the run shown.
// replayEnd is the time of the last event shown while replaying; live mode leaves it out.
export function RunStats({ run, replayEnd }: { run: RunView; replayEnd?: number }) {
  const ticking = !run.endedAt && replayEnd === undefined && run.status === "running";
  const now = useNow(ticking);
  const end = run.endedAt ? Date.parse(run.endedAt) : (replayEnd ?? now);
  const done = run.graph.nodes.filter((n) => run.nodes[n.id]?.status === "succeeded").length;
  const pct = run.budgetUSD ? Math.min(100, (run.costUSD / run.budgetUSD) * 100) : 0;
  return (
    <div className="stats">
      <span>{clock(end - Date.parse(run.startedAt))}</span>
      <span>
        {done} of {run.graph.nodes.length} <span className="unit">nodes</span>
      </span>
      <span className="cost">
        <span>
          ${run.costUSD.toFixed(2)}
          {run.budgetUSD ? ` of $${run.budgetUSD.toFixed(2)}` : ""}
        </span>
        {run.budgetUSD ? (
          <span className="bar" aria-hidden="true">
            <span style={{ width: `${pct}%` }} />
          </span>
        ) : null}
      </span>
    </div>
  );
}
