import type { FactoryEvent, FailureClass, GateBrief, GateDecidedData, Graph, NodeStatus, RunStatus } from "./gen/events";

export interface NodeView {
  status: NodeStatus;
  attempt: number;
  taskFailures: number;
  infraFailures: number;
  lastError?: string;
  costUSD: number;
  logs: string[];
  startedAt?: string; // `at` of the last node_started
  endedAt?: string; // `at` of the last node_succeeded, node_failed or gate_decided
  revisionsLeft?: number; // from gate_waiting; undefined for an old log
  brief?: GateBrief; // from gate_waiting; undefined for an old log
  decision?: GateDecidedData & { at: string }; // from gate_decided; cleared by gate_waiting
}

export interface RunView {
  id: string;
  graph: Graph;
  graphVersion: number;
  versions: GraphVersion[]; // every graph the run has had, oldest first
  status: RunStatus;
  reason?: string;
  costUSD: number;
  nodes: Record<string, NodeView>;
  startedAt: string; // `at` of run_created
  endedAt?: string; // `at` of run_finished
  budgetUSD?: number; // run_created guardrails.budget.run_usd, if it is more than 0
}

// GraphVersion is a graph the run had, and why it changed.
export interface GraphVersion {
  version: number;
  graph: Graph;
  reason: string;
  replaced: string[]; // tasks a replan took out
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
    const budget = e.data.guardrails?.budget?.run_usd ?? 0;
    const first = { version: 1, graph: e.data.graph, reason: "the graph the run started with", replaced: [] };
    return { id: e.run, graph: e.data.graph, graphVersion: 1, versions: [first], status: "running", costUSD: 0, nodes, startedAt: e.at, budgetUSD: budget > 0 ? budget : undefined };
  }
  if (!run) return run;
  if (e.type === "graph_updated") {
    // A replaced task's state ends with it, so a new node never inherits it.
    const replaced = new Set(e.data.replaced ?? []);
    const nodes = Object.fromEntries(e.data.graph.nodes.map((n) => [n.id, (!replaced.has(n.id) && run.nodes[n.id]) || blank()]));
    // The plan node that emitted the nodes succeeds in this same event.
    const cost = e.data.cost_usd ?? 0;
    if (e.node && nodes[e.node]) nodes[e.node] = { ...nodes[e.node], status: "succeeded", costUSD: nodes[e.node].costUSD + cost };
    const version = e.data.version ?? run.graphVersion + 1; // logs from before versions have none
    const next = { version, graph: e.data.graph, reason: e.data.reason, replaced: e.data.replaced ?? [] };
    return { ...run, graph: e.data.graph, graphVersion: version, versions: [...run.versions, next], costUSD: run.costUSD + cost, nodes };
  }
  if (e.type === "run_finished") {
    return { ...run, status: e.data.status, reason: e.data.reason, endedAt: e.at };
  }

  if (e.type === "gate_decided" && e.data.revise && e.node) {
    // The runtime sent the work back: the gate and the nodes it waits for run again.
    const gate = run.graph.nodes.find((g) => g.id === e.node);
    const nodes = { ...run.nodes, [e.node]: { ...run.nodes[e.node], status: "pending" as const, endedAt: e.at, decision: { ...e.data, at: e.at } } };
    for (const id of gate?.deps ?? []) {
      if (nodes[id]) nodes[id] = { ...nodes[id], status: "pending", taskFailures: 0, lastError: undefined };
    }
    return { ...run, nodes };
  }

  const n = e.node ? run.nodes[e.node] : undefined;
  if (!e.node || !n) return run;
  let next: NodeView;
  switch (e.type) {
    case "node_started":
      next = { ...n, status: "running", attempt: e.attempt ?? n.attempt, startedAt: e.at, logs: [...n.logs, `── attempt ${e.attempt} ──`] };
      break;
    case "node_log":
      next = { ...n, logs: [...n.logs, e.data.line].slice(-maxLogLines) };
      break;
    case "node_succeeded":
      next = { ...n, status: "succeeded", endedAt: e.at, costUSD: n.costUSD + (e.data?.cost_usd ?? 0) };
      break;
    case "node_failed":
      next = {
        ...n,
        status: e.data.will_retry ? "pending" : e.data.abandoned ? "succeeded" : "failed",
        endedAt: e.at,
        lastError: e.data.error,
        taskFailures: n.taskFailures + (e.data.class === "task" ? 1 : 0),
        infraFailures: n.infraFailures + (e.data.class === "infra" ? 1 : 0),
        costUSD: n.costUSD + (e.data.cost_usd ?? 0),
        logs: [...n.logs, `✗ ${failureKind(e.data.class)} failure: ${e.data.error}${e.data.will_retry ? " (will retry)" : e.data.abandoned ? " (advisory: the run goes on)" : ""}`],
      };
      break;
    case "gate_waiting":
      next = { ...n, status: "waiting", revisionsLeft: e.data?.revisions_left, brief: e.data?.brief, decision: undefined };
      break;
    case "gate_decided": {
      const decision = { ...e.data, at: e.at };
      next = e.data.approved
        ? { ...n, status: "succeeded", endedAt: e.at, decision }
        : { ...n, status: "failed", endedAt: e.at, decision, lastError: `rejected: ${e.data.notes ?? ""}` };
      break;
    }
    default:
      return run;
  }
  const spent = next.costUSD - n.costUSD;
  return { ...run, costUSD: run.costUSD + spent, nodes: { ...run.nodes, [e.node]: next } };
}

export interface GraphDiff {
  added: string[];
  removed: string[];
  changed: string[]; // the node stays, but its dependencies or scope differ
}

// diffGraphs says what a later version of a graph added, removed and changed.
export function diffGraphs(from: Graph, to: Graph): GraphDiff {
  const before = new Map(from.nodes.map((n) => [n.id, n]));
  const after = new Map(to.nodes.map((n) => [n.id, n]));
  const key = (n: Graph["nodes"][number]) => JSON.stringify([n.deps ?? [], n.scope ?? []]);
  return {
    added: to.nodes.filter((n) => !before.has(n.id)).map((n) => n.id),
    removed: from.nodes.filter((n) => !after.has(n.id)).map((n) => n.id),
    changed: to.nodes.filter((n) => before.has(n.id) && key(before.get(n.id)!) !== key(n)).map((n) => n.id),
  };
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

const count = (n: number) => (n ? ` (${n} comment${n > 1 ? "s" : ""})` : "");
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
      return `${e.node} failed (${failureKind(e.data.class)}${e.data.will_retry ? ", will retry" : e.data.abandoned ? ", the run goes on" : ""}): ${e.data.error}`;
    case "gate_waiting":
      return `${e.node} waits for you`;
    case "gate_decided":
      return `${e.node} ${e.data.approved ? "approved" : e.data.revise ? "sent back" : "rejected"}${e.data.notes ? `: ${e.data.notes}` : ""}${count(e.data.comments?.length ?? 0)}`;
    case "run_finished":
      return `run ${e.data.status}${e.data.reason ? `: ${e.data.reason}` : ""}`;
  }
}
