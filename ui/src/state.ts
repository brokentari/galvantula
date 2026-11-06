import type { FactoryEvent, FailureClass, Graph, NodeStatus, RunStatus } from "./gen/events";

export interface NodeView {
  status: NodeStatus;
  attempt: number;
  taskFailures: number;
  infraFailures: number;
  lastError?: string;
  costUSD: number;
  logs: string[];
}

export interface RunView {
  id: string;
  graph: Graph;
  graphVersion: number;
  status: RunStatus;
  reason?: string;
  costUSD: number;
  nodes: Record<string, NodeView>;
}

const maxLogLines = 200;

const blank = (): NodeView => ({ status: "pending", attempt: 0, taskFailures: 0, infraFailures: 0, costUSD: 0, logs: [] });

export function fold(events: FactoryEvent[]): RunView | undefined {
  let run: RunView | undefined;
  for (const e of events) run = apply(run, e);
  return run;
}

// apply mirrors State.Apply in internal/event. Events carry every decision
// (such as will_retry), so this stays a plain mapping with no policy.
export function apply(run: RunView | undefined, e: FactoryEvent): RunView | undefined {
  if (e.type === "run_created") {
    const nodes = Object.fromEntries(e.data.graph.nodes.map((n) => [n.id, blank()]));
    return { id: e.run, graph: e.data.graph, graphVersion: 1, status: "running", costUSD: 0, nodes };
  }
  if (!run) return run;
  if (e.type === "graph_updated") {
    const nodes = Object.fromEntries(e.data.graph.nodes.map((n) => [n.id, run.nodes[n.id] ?? blank()]));
    // The plan node that emitted the nodes succeeds in this same event.
    const cost = e.data.cost_usd ?? 0;
    if (e.node && nodes[e.node]) nodes[e.node] = { ...nodes[e.node], status: "succeeded", costUSD: nodes[e.node].costUSD + cost };
    return { ...run, graph: e.data.graph, graphVersion: run.graphVersion + 1, costUSD: run.costUSD + cost, nodes };
  }
  if (e.type === "run_finished") {
    return { ...run, status: e.data.status, reason: e.data.reason };
  }

  const n = e.node ? run.nodes[e.node] : undefined;
  if (!e.node || !n) return run;
  let next: NodeView;
  switch (e.type) {
    case "node_started":
      next = { ...n, status: "running", attempt: e.attempt ?? n.attempt, logs: [...n.logs, `── attempt ${e.attempt} ──`] };
      break;
    case "node_log":
      next = { ...n, logs: [...n.logs, e.data.line].slice(-maxLogLines) };
      break;
    case "node_succeeded":
      next = { ...n, status: "succeeded", costUSD: n.costUSD + (e.data?.cost_usd ?? 0) };
      break;
    case "node_failed":
      next = {
        ...n,
        status: e.data.will_retry ? "pending" : "failed",
        lastError: e.data.error,
        taskFailures: n.taskFailures + (e.data.class === "task" ? 1 : 0),
        infraFailures: n.infraFailures + (e.data.class === "infra" ? 1 : 0),
        costUSD: n.costUSD + (e.data.cost_usd ?? 0),
        logs: [...n.logs, `✗ ${failureKind(e.data.class)} failure: ${e.data.error}${e.data.will_retry ? " (will retry)" : ""}`],
      };
      break;
    case "gate_waiting":
      next = { ...n, status: "waiting" };
      break;
    case "gate_decided":
      next = e.data.approved
        ? { ...n, status: "succeeded" }
        : { ...n, status: "failed", lastError: `rejected: ${e.data.notes ?? ""}` };
      break;
    default:
      return run;
  }
  const spent = next.costUSD - n.costUSD;
  return { ...run, costUSD: run.costUSD + spent, nodes: { ...run.nodes, [e.node]: next } };
}

// layout puts each node in the column of its longest dependency chain.
export function layout(graph: Graph, colWidth = 205, rowHeight = 84): Map<string, { x: number; y: number }> {
  const byId = new Map(graph.nodes.map((n) => [n.id, n]));
  const depth = new Map<string, number>();
  const depthOf = (id: string): number => {
    const known = depth.get(id);
    if (known !== undefined) return known;
    const deps = byId.get(id)?.deps ?? [];
    const d = deps.length ? 1 + Math.max(...deps.map(depthOf)) : 0;
    depth.set(id, d);
    return d;
  };
  const columns = new Map<number, string[]>();
  for (const n of graph.nodes) {
    const d = depthOf(n.id);
    columns.set(d, [...(columns.get(d) ?? []), n.id]);
  }
  const pos = new Map<string, { x: number; y: number }>();
  for (const [d, ids] of columns) {
    ids.forEach((id, i) => pos.set(id, { x: d * colWidth, y: (i - (ids.length - 1) / 2) * rowHeight }));
  }
  return pos;
}

const failureKind = (c: FailureClass) => ({ task: "task", infra: "infrastructure", limit: "usage limit" })[c];

export function describe(e: FactoryEvent): string {
  switch (e.type) {
    case "run_created":
      return `run created from graph "${e.data.graph.name}"`;
    case "graph_updated":
      return e.data.reason;
    case "node_started":
      return `${e.node} started (attempt ${e.attempt})`;
    case "node_log":
      return `${e.node}: ${e.data.line}`;
    case "node_succeeded":
      return `${e.node} succeeded`;
    case "node_failed":
      return `${e.node} failed (${failureKind(e.data.class)}${e.data.will_retry ? ", will retry" : ""}): ${e.data.error}`;
    case "gate_waiting":
      return `${e.node} waits for you`;
    case "gate_decided":
      return `${e.node} ${e.data.approved ? "approved" : "rejected"}${e.data.notes ? `: ${e.data.notes}` : ""}`;
    case "run_finished":
      return `run ${e.data.status}${e.data.reason ? `: ${e.data.reason}` : ""}`;
  }
}
