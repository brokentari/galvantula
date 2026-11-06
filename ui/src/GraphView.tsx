import { Background, Controls, Handle, Position, ReactFlow, type Edge, type Node, type NodeProps } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { useMemo } from "react";
import type { GraphNode } from "./gen/events";
import { layout, type NodeView, type RunView } from "./state";

type CardData = { node: GraphNode; view: NodeView; selected: boolean };

function Card({ data }: NodeProps<Node<CardData>>) {
  const { node, view, selected } = data;
  return (
    <div className={`card kind-${node.kind} status-${view.status}${selected ? " selected" : ""}`}>
      <Handle type="target" position={Position.Left} />
      <div className="card-id">{node.id}</div>
      {node.title && <div className="card-title">{node.title}</div>}
      <div className="card-meta">
        <span className="dot" />
        {node.kind} · {view.status}
        {view.attempt > 1 && ` · try ${view.attempt}`}
      </div>
      <Handle type="source" position={Position.Right} />
    </div>
  );
}

const nodeTypes = { card: Card };

export function GraphView({ run, selected, onSelect }: { run: RunView; selected?: string; onSelect: (id: string) => void }) {
  const { nodes, edges } = useMemo(() => {
    const pos = layout(run.graph);
    const nodes: Node<CardData>[] = run.graph.nodes.map((n) => ({
      id: n.id,
      type: "card",
      position: pos.get(n.id) ?? { x: 0, y: 0 },
      data: { node: n, view: run.nodes[n.id], selected: n.id === selected },
    }));
    const edges: Edge[] = run.graph.nodes.flatMap((n) =>
      (n.deps ?? []).map((d) => ({
        id: `${d}->${n.id}`,
        source: d,
        target: n.id,
        animated: run.nodes[n.id]?.status === "running",
        className: run.nodes[d]?.status === "succeeded" ? "edge-done" : "",
      })),
    );
    return { nodes, edges };
  }, [run, selected]);

  return (
    <ReactFlow
      // Remount when the plan changes the graph, so the view fits the new shape.
      key={`${run.id}:${run.graphVersion}`}
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
