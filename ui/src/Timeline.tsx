import { useMemo, type ReactNode } from "react";
import type { FactoryEvent } from "./gen/events";

const kinds: Record<string, string> = { gate_waiting: "gate", node_failed: "fail", graph_updated: "graph" };

// Timeline draws marks over the slider (its child) where a gate waited, a node failed or the graph changed.
// events grows in place, so count keys the memo.
export function Timeline({ events, count, children }: { events: FactoryEvent[]; count: number; children: ReactNode }) {
  const marks = useMemo(
    () => events.flatMap((e, i) => (kinds[e.type] ? [{ seq: e.seq, kind: kinds[e.type], at: ((i + 1) / events.length) * 100 }] : [])),
    [events, count],
  );
  return (
    <div className="timeline">
      <div className="marks" aria-hidden="true">
        {marks.map((m) => (
          <span key={m.seq} className={`mark mark-${m.kind}`} style={{ left: `${m.at}%` }} />
        ))}
      </div>
      {children}
    </div>
  );
}
