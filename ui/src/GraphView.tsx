import { Background, Controls, Handle, Position, ReactFlow, type Edge, type Node, type NodeProps } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { useMemo } from "react";
import type { Graph, GraphNode } from "./gen/events";
import { layout, type NodeView, type RunView } from "./state";

// Marks say how a graph version differs from the one before it.
export type Marks = Record<string, "added" | "removed" | "changed">;

const noView: NodeView = { status: "pending", attempt: 0, taskFailures: 0, infraFailures: 0, costUSD: 0, logs: [] };

type CardData = { node: GraphNode; view: NodeView; selected: boolean; mark?: string };

function Card({ data }: NodeProps<Node<CardData>>) {
  const { node, view, selected, mark } = data;
  return (
    <div className={`card kind-${node.kind} status-${view.status}${selected ? " selected" : ""}${mark ? ` mark-${mark}` : ""}`}>
      <Handle type="target" position={Position.Left} />
      <div className="card-id">{node.id}</div>
      {node.title && <div className="card-title">{node.title}</div>}
      <div className="card-meta">
        <span className="dot" />
        {node.kind} · {mark === "removed" ? "replaced" : view.status}
        {view.attempt > 1 && ` · try ${view.attempt}`}
      </div>
      <Handle type="source" position={Position.Right} />
    </div>
  );
}

const nodeTypes = { card: Card };

export function GraphView({ run, graph, marks, selected, onSelect }: { run: RunView; graph: Graph; marks: Marks; selected?: string; onSelect: (id: string) => void }) {
  // The layout depends only on the graph, which changes only when a plan adds or replaces nodes.
  const pos = useMemo(() => layout(graph), [graph]);
  const { nodes, edges } = useMemo(() => {
    const nodes: Node<CardData>[] = graph.nodes.map((n) => ({
      id: n.id,
      type: "card",
      position: pos.get(n.id) ?? { x: 0, y: 0 },
      data: { node: n, view: run.nodes[n.id] ?? noView, selected: n.id === selected, mark: marks[n.id] },
    }));
    const edges: Edge[] = graph.nodes.flatMap((n) =>
      (n.deps ?? []).map((d) => ({
        id: `${d}->${n.id}`,
        source: d,
        target: n.id,
        animated: run.nodes[n.id]?.status === "running",
        className: run.nodes[d]?.status === "succeeded" ? "edge-done" : "",
      })),
    );
    return { nodes, edges };
  }, [run, graph, marks, pos, selected]);

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
      fitView
      fitViewOptions={{ padding: 0.08 }}
      minZoom={0.1}
    >
      <Background gap={24} size={1} />
      <Controls showInteractive={false} />
    </ReactFlow>
  );
}
