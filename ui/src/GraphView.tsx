import { Background, Controls, Handle, MiniMap, Position, ReactFlow, useReactFlow, useStore, type Edge, type Node, type NodeProps } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import "./graph.css";
import { useEffect, useMemo } from "react";
import type { Graph, GraphNode } from "./gen/events";
import { layout, type NodeView, type RunView } from "./state";
import { clock, useNow } from "./time";

// Marks say how a graph version differs from the one before it.
export type Marks = Record<string, "added" | "removed" | "changed">;

const noView: NodeView = { status: "pending", attempt: 0, taskFailures: 0, infraFailures: 0, costUSD: 0, logs: [] };

type CardData = { node: GraphNode; view: NodeView; selected: boolean; mark?: string; review?: "review" | "dim"; now: number };

// elapsed gives the time part of the meta line: none for a gate, a pending node or a node with no start.
function elapsed({ node, view, now }: CardData): string {
  if (node.kind === "gate" || !view.startedAt) return "";
  const start = Date.parse(view.startedAt);
  if (view.status === "running") return ` · ${clock(now - start)}`;
  if ((view.status !== "succeeded" && view.status !== "failed") || !view.endedAt) return "";
  return ` · ${clock(Date.parse(view.endedAt) - start)}${view.costUSD > 0 ? ` · $${view.costUSD.toFixed(2)}` : ""}`;
}

function Card({ data }: NodeProps<Node<CardData>>) {
  const { node, view, selected, mark, review } = data;
  return (
    <div className={`card kind-${node.kind} status-${view.status}${selected ? " selected" : ""}${mark ? ` mark-${mark}` : ""}${review ? ` ${review}` : ""}`}>
      <Handle type="target" position={Position.Left} />
      <div className="card-id">{node.id}</div>
      {node.title && <div className="card-title">{node.title}</div>}
      <div className="card-meta">
        <span className="dot" />
        {node.kind} · {mark === "removed" ? "replaced" : view.status}
        {elapsed(data)}
        {view.attempt > 1 && ` · try ${view.attempt}`}
      </div>
      <Handle type="source" position={Position.Right} />
    </div>
  );
}

const nodeTypes = { card: Card };

// Follow keeps the nodes that matter in view at a size you can read: the nodes the person
// opened, else the ones that run, else the gates that wait, else the whole graph. It moves
// when that set changes or the canvas resizes, as it does when a gate widens the panel.
function Follow({ ids }: { ids: string[] }) {
  const { fitView } = useReactFlow();
  // React Flow measures the canvas itself, so the size here is never older than its own state.
  const width = useStore((s) => s.width);
  const height = useStore((s) => s.height);
  const key = ids.join(",");
  useEffect(() => {
    if (!width || !height) return;
    fitView({ nodes: key ? key.split(",").map((id) => ({ id })) : undefined, padding: 0.25, minZoom: key ? 0.9 : 0.1, maxZoom: 1, duration: 300 });
  }, [width, height, key, fitView]);
  return null;
}

// now is the time running cards measure against while a run replays; leave it out in live mode.
export function GraphView({ run, graph, marks, selected, onSelect, now }: { run: RunView; graph: Graph; marks: Marks; selected?: string; onSelect: (id: string) => void; now?: number }) {
  // The layout depends only on the graph, which changes only when a plan adds or replaces nodes.
  const pos = useMemo(() => layout(graph), [graph]);
  const tick = useNow(now === undefined && Object.values(run.nodes).some((v) => v.status === "running"));
  const t = now ?? tick;
  const { nodes, edges, focus } = useMemo(() => {
    // A waiting gate asks the person to approve its deps; those are under review.
    const gate = graph.nodes.find((n) => n.id === selected);
    const review = gate && run.nodes[gate.id]?.status === "waiting" ? new Set(gate.deps ?? []) : undefined;
    const withStatus = (st: string) => graph.nodes.filter((n) => run.nodes[n.id]?.status === st).map((n) => n.id);
    const focus = gate ? [gate.id, ...(review ?? [])] : selected && run.nodes[selected] ? [selected] : run.status === "running" ? [...withStatus("running"), ...withStatus("waiting")] : [];
    const nodes: Node<CardData>[] = graph.nodes.map((n) => ({
      id: n.id,
      type: "card",
      position: pos.get(n.id) ?? { x: 0, y: 0 },
      data: {
        node: n,
        view: run.nodes[n.id] ?? noView,
        selected: n.id === selected,
        mark: marks[n.id],
        now: t,
        review: review && n !== gate ? (review.has(n.id) ? "review" : "dim") : undefined,
      },
    }));
    const edges: Edge[] = graph.nodes.flatMap((n) =>
      (n.deps ?? []).map((d) => ({
        id: `${d}->${n.id}`,
        source: d,
        target: n.id,
        animated: run.nodes[n.id]?.status === "running",
        className: review && n === gate ? "edge-review" : run.nodes[d]?.status === "succeeded" ? "edge-done" : "",
      })),
    );
    return { nodes, edges, focus };
  }, [run, graph, marks, pos, selected, t]);

  return (
    <ReactFlow
      // Remount when the plan changes the graph, so the view fits the new shape.
      key={`${run.id}:${graph.nodes.length}:${Object.keys(marks).length}:${run.graphVersion}`}
      nodes={nodes}
      edges={edges}
      nodeTypes={nodeTypes}
      onNodeClick={(_, n) => onSelect(n.id)}
      nodesDraggable={false}
      nodesConnectable={false}
      elementsSelectable={false}
      minZoom={0.1}
    >
      <Background gap={24} size={1} />
      <Controls showInteractive={false} />
      <Follow ids={focus} />
      {graph.nodes.length > 12 && <MiniMap pannable zoomable />}
    </ReactFlow>
  );
}
