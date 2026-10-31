import { memo, useCallback, useEffect, useMemo, useState, type DragEvent } from "react";
import { useTranslation } from "react-i18next";
import {
  Background,
  Controls,
  Handle,
  MarkerType,
  Position,
  ReactFlow,
  ReactFlowProvider,
  applyEdgeChanges,
  applyNodeChanges,
  useReactFlow,
  type Connection,
  type Edge,
  type EdgeChange,
  type Node,
  type NodeChange,
  type NodeProps,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import Icon, { type IconName } from "./Icon";
import {
  NODE_TYPES,
  belowAll,
  layout,
  link,
  outlets,
  positions,
  summary,
  type BotFlow,
  type BotNode,
  type NodeType,
  withoutNode,
  type XY,
} from "../lib/bots";

// FlowCanvas shows a chatbot flow as boxes you can drag, link and add to. It edits the same
// flow the step list below it edits: a link drawn here sets that step's "Then go to".

const DRAG_TYPE = "application/x-ecogo-bot-step";

const STEP_ICON: Record<NodeType, IconName> = {
  message: "message",
  buttons: "list",
  question: "help",
  condition: "branch",
  set: "variable",
  tag: "tag",
  template: "file",
  flow: "flow",
  ai: "sparkles",
  handoff: "headset",
  end: "stop",
};

type Side = "left" | "right";
const SIDE_KEY = "ecogo.botToolboxSide";

function savedSide(): Side {
  try {
    return localStorage.getItem(SIDE_KEY) === "right" ? "right" : "left";
  } catch {
    return "left";
  }
}

type StepData = {
  id: string;
  node: BotNode;
  isStart: boolean;
  reachable: boolean;
  hasProblems: boolean;
};
type StepNode = Node<StepData, "step">;

export interface FlowCanvasProps {
  flow: BotFlow;
  selected: string | null;
  problemNodes: Set<string>;
  onSelect: (id: string | null) => void;
  onChange: (flow: BotFlow) => void;
  /** Adds a step of this type at a canvas position and returns its id. */
  onAdd: (type: NodeType, at: XY) => void;
}

export default function FlowCanvas(props: FlowCanvasProps) {
  return (
    <ReactFlowProvider>
      <Canvas {...props} />
    </ReactFlowProvider>
  );
}

const nodeTypes = { step: memo(StepBox) };

function Canvas({ flow, selected, problemNodes, onSelect, onChange, onAdd }: FlowCanvasProps) {
  const { t } = useTranslation();
  const rf = useReactFlow();
  const [side, setSide] = useState<Side>(savedSide);
  const flip = () => {
    const next: Side = side === "left" ? "right" : "left";
    setSide(next);
    try {
      localStorage.setItem(SIDE_KEY, next);
    } catch {
      // storage blocked: the choice lasts until the page closes
    }
  };

  const built = useMemo(() => {
    const at = positions(flow);
    const reach = new Map(layout(flow).map((p) => [p.id, p.reachable]));
    const nodes: StepNode[] = Object.entries(flow.nodes).map(([id, n]) => ({
      id,
      type: "step",
      position: at[id],
      selected: id === selected,
      data: { id, node: n, isStart: flow.start === id, reachable: reach.get(id) ?? false, hasProblems: problemNodes.has(id) },
    }));
    const edges: Edge[] = [];
    for (const [id, n] of Object.entries(flow.nodes)) {
      const outs = outlets(n);
      for (const o of outs) {
        if (!o.target || !(o.target in flow.nodes)) continue;
        edges.push({
          id: `${id}|${o.handle}`,
          source: id,
          sourceHandle: o.handle,
          target: o.target,
          label: outs.length > 1 ? outletLabel(o.handle, o.label, t) : undefined,
          markerEnd: { type: MarkerType.ArrowClosed },
        });
      }
    }
    return { nodes, edges };
  }, [flow, selected, problemNodes, t]);

  // React Flow keeps its own copy so drags and selections stay smooth; the flow is the source
  // of truth and replaces the copy whenever it changes.
  const [nodes, setNodes] = useState<StepNode[]>(built.nodes);
  const [edges, setEdges] = useState<Edge[]>(built.edges);
  useEffect(() => {
    setNodes(built.nodes);
    setEdges(built.edges);
  }, [built]);

  const onNodesChange = useCallback((changes: NodeChange<StepNode>[]) => {
    // Removals go through onNodesDelete so links to the step are cleaned up too.
    setNodes((ns) => applyNodeChanges(changes.filter((c) => c.type !== "remove"), ns));
  }, []);
  const onEdgesChange = useCallback((changes: EdgeChange[]) => {
    setEdges((es) => applyEdgeChanges(changes.filter((c) => c.type !== "remove"), es));
  }, []);

  // Saving every node's place on the first drag pins the automatic layout, so other steps do
  // not jump when links change later.
  const onNodeDragStop = useCallback(
    (_: unknown, __: StepNode, dragged: StepNode[]) => {
      const moved = new Map(dragged.map((n) => [n.id, n.position]));
      const next: Record<string, XY> = {};
      for (const n of nodes) {
        const p = moved.get(n.id) ?? n.position;
        next[n.id] = { x: Math.round(p.x), y: Math.round(p.y) };
      }
      onChange({ ...flow, layout: next });
    },
    [flow, nodes, onChange],
  );

  const onConnect = useCallback(
    (c: Connection) => {
      if (!c.source || !c.target || !c.sourceHandle || c.source === c.target) return;
      const n = flow.nodes[c.source];
      if (!n) return;
      onChange({ ...flow, nodes: { ...flow.nodes, [c.source]: link(n, c.sourceHandle, c.target) } });
    },
    [flow, onChange],
  );

  // One change for a delete: removing a step also removes the links to it, and a removed link
  // clears that step's "Then go to".
  const onDelete = useCallback(
    ({ nodes: goneNodes, edges: goneEdges }: { nodes: StepNode[]; edges: Edge[] }) => {
      let next = flow;
      const gone = new Set(goneNodes.map((n) => n.id));
      for (const id of gone) next = withoutNode(next, id);
      const nodesNext = { ...next.nodes };
      for (const e of goneEdges) {
        const n = nodesNext[e.source];
        if (n && e.sourceHandle && !gone.has(e.source) && !gone.has(e.target)) nodesNext[e.source] = link(n, e.sourceHandle, "");
      }
      if (gone.has(selected ?? "")) onSelect(null);
      onChange({ ...next, nodes: nodesNext });
    },
    [flow, selected, onChange, onSelect],
  );

  const onDrop = (e: DragEvent) => {
    const type = e.dataTransfer.getData(DRAG_TYPE) as NodeType;
    if (!NODE_TYPES.includes(type)) return;
    e.preventDefault();
    const p = rf.screenToFlowPosition({ x: e.clientX, y: e.clientY });
    onAdd(type, { x: Math.round(p.x - 90), y: Math.round(p.y - 30) });
  };

  return (
    <div className="flow-canvas">
      <div className={`canvas-row side-${side}`}>
        <div className={`canvas-toolbox side-${side}`} role="toolbar" aria-orientation="vertical" aria-label={t("bots.canvasPalette")}>
          <button type="button" className="toolbox-side" onClick={flip} title={t(side === "left" ? "bots.toolboxRight" : "bots.toolboxLeft")} aria-label={t(side === "left" ? "bots.toolboxRight" : "bots.toolboxLeft")}>
            <Icon name={side === "left" ? "panelRight" : "panelLeft"} size="s" />
          </button>
          {NODE_TYPES.map((type) => (
            <button
              key={type}
              type="button"
              className={`tool step-${type}`}
              draggable
              title={`${t(`bots.node_${type}`)}: ${t("bots.canvasPaletteHint")}`}
              aria-label={t(`bots.node_${type}`)}
              onDragStart={(e) => {
                e.dataTransfer.setData(DRAG_TYPE, type);
                e.dataTransfer.effectAllowed = "move";
              }}
              onClick={() => onAdd(type, belowAll(flow))}
            >
              <Icon name={STEP_ICON[type]} />
              <span className="tool-label">{t(`bots.node_${type}`)}</span>
            </button>
          ))}
        </div>
        <div className="canvas-area" onDragOver={(e) => { e.preventDefault(); e.dataTransfer.dropEffect = "move"; }} onDrop={onDrop}>
          <ReactFlow<StepNode, Edge>
            nodes={nodes}
            edges={edges}
            nodeTypes={nodeTypes}
            onNodesChange={onNodesChange}
            onEdgesChange={onEdgesChange}
            onNodeDragStop={onNodeDragStop}
            onConnect={onConnect}
            onDelete={onDelete}
            onNodeClick={(_, n) => onSelect(n.id)}
            onPaneClick={() => onSelect(null)}
            isValidConnection={(c) => c.source !== c.target}
            fitView
            fitViewOptions={{ maxZoom: 1, padding: 0.2 }}
            minZoom={0.25}
            maxZoom={1.5}
            colorMode="system"
            deleteKeyCode={["Backspace", "Delete"]}
            defaultEdgeOptions={{ type: "smoothstep" }}
          >
            <Background gap={20} />
            <Controls showInteractive={false} />
          </ReactFlow>
        </div>
      </div>
      <p className="muted small">
        {t("bots.canvasHelp")}
        {nodes.some((n) => !n.data.reachable) && <> {t("bots.unreachable")}</>}
      </p>
    </div>
  );
}

function outletLabel(handle: string, label: string, t: (k: string) => string): string {
  if (handle === "then") return t("bots.then");
  if (handle === "else") return t("bots.else");
  if (handle === "next") return t("bots.next");
  return label.slice(0, 20);
}

function StepBox({ data }: NodeProps<StepNode>) {
  const { t } = useTranslation();
  const { id, node, isStart, reachable, hasProblems } = data;
  const outs = outlets(node);
  const text = summary(node);
  return (
    <div className={`step-box step-${node.type} ${reachable ? "" : "lost"} ${hasProblems ? "has-problems" : ""}`}>
      <Handle type="target" position={Position.Left} />
      <div className="step-head">
        <span className={`step-icon step-${node.type}`}><Icon name={STEP_ICON[node.type]} size="xs" /></span>
        <span className="step-type">{t(`bots.node_${node.type}`)}</span>
        {isStart && <span className="pill t-approved">{t("bots.start")}</span>}
        {hasProblems && <span className="field-error" aria-label={t("bots.problems")}>!</span>}
      </div>
      <strong className="step-id">{id}</strong>
      {text && <div className="step-text small">{text.slice(0, 60)}</div>}
      {outs.length > 0 && (
        <div className="step-outlets">
          {outs.map((o) => (
            <div key={o.handle} className="step-outlet small">
              <span>{outletLabel(o.handle, o.label, t)}</span>
              <Handle type="source" id={o.handle} position={Position.Right} />
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
