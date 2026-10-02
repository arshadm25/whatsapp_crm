// Chatbot flows as the api stores them, plus the helpers the builder needs: new nodes, links,
// a left-to-right layout for the flow map, and the same checks the api makes on save.

export type NodeType =
  | "message"
  | "buttons"
  | "question"
  | "condition"
  | "set"
  | "tag"
  | "template"
  | "handoff"
  | "end";

export const NODE_TYPES: NodeType[] = ["message", "buttons", "question", "condition", "set", "tag", "template", "handoff", "end"];

export interface BotButton {
  id: string;
  title: string;
  next?: string;
}

export interface BotNode {
  type: NodeType;
  text?: string;
  next?: string;
  buttons?: BotButton[];
  var?: string;
  kind?: "text" | "number" | "email" | "phone";
  op?: "equals" | "not_equals" | "contains" | "exists" | "gt" | "lt";
  value?: string;
  then?: string;
  else?: string;
  tag?: string;
  template?: { name: string; language: string; params?: string[] };
  reason?: string;
  assign_to?: string;
}

export type TriggerType = "keyword" | "first_message" | "button_reply" | "any_message";

export interface BotTrigger {
  type: TriggerType;
  keywords?: string[];
  match?: "exact" | "contains";
  button_id?: string;
}

export interface BotFlow {
  start: string;
  triggers: BotTrigger[];
  nodes: Record<string, BotNode>;
}

export type BotStatus = "draft" | "active" | "paused";

export interface Bot {
  id: string;
  name: string;
  status: BotStatus;
  phone_number_id: string | null;
  flow: BotFlow;
  created_at: string;
  updated_at: string;
}

export interface BotSession {
  id: string;
  bot_id: string;
  conversation_id: string;
  contact_id: string;
  status: "active" | "completed" | "handed_off" | "stopped" | "expired" | "failed";
  end_reason: string | null;
  variables: Record<string, string>;
  started_at: string;
  ended_at: string | null;
}

export const MAX_NODES = 100;
export const MAX_BUTTONS = 3;
export const MAX_BUTTON_TITLE = 20;
export const MAX_BODY = 1024;
export const MAX_TEXT = 4096;

export const TRIGGER_TYPES: TriggerType[] = ["keyword", "first_message", "button_reply", "any_message"];

export function newNode(type: NodeType): BotNode {
  switch (type) {
    case "message":
      return { type, text: "" };
    case "buttons":
      return { type, text: "", buttons: [{ id: "yes", title: "Yes" }] };
    case "question":
      return { type, text: "", var: "answer", kind: "text" };
    case "condition":
      return { type, var: "answer", op: "equals", value: "", then: "" };
    case "set":
      return { type, var: "answer", value: "" };
    case "tag":
      return { type, tag: "" };
    case "template":
      return { type, template: { name: "", language: "en", params: [] } };
    case "handoff":
      return { type, text: "", reason: "wants_agent" };
    case "end":
      return { type };
  }
}

export function emptyFlow(): BotFlow {
  return {
    start: "welcome",
    triggers: [{ type: "keyword", keywords: ["hi"], match: "exact" }],
    nodes: { welcome: { type: "message", text: "Hello {{contact.name}}! How can we help?" } },
  };
}

// nextId returns an unused id such as "question_2".
export function nextId(flow: BotFlow, type: NodeType): string {
  for (let n = 1; ; n++) {
    const id = `${type}_${n}`;
    if (!(id in flow.nodes)) return id;
  }
}

export interface Edge {
  /** What the link is for: "next", "then", "else" or a button title. */
  label: string;
  target: string;
}

export function edges(node: BotNode): Edge[] {
  const out: Edge[] = [];
  const add = (label: string, target?: string) => {
    if (target) out.push({ label, target });
  };
  if (node.type === "condition") {
    add("then", node.then);
    add("else", node.else);
  } else if (node.type === "buttons") {
    for (const b of node.buttons ?? []) add(b.title, b.next);
  } else {
    add("next", node.next);
  }
  return out;
}

// withoutNode removes a node and every link that pointed to it. The start moves to another node
// if it was the one removed.
export function withoutNode(flow: BotFlow, id: string): BotFlow {
  const nodes: Record<string, BotNode> = {};
  for (const [nid, n] of Object.entries(flow.nodes)) {
    if (nid === id) continue;
    const c: BotNode = { ...n };
    if (c.next === id) delete c.next;
    if (c.then === id) c.then = "";
    if (c.else === id) delete c.else;
    if (c.buttons) c.buttons = c.buttons.map((b) => (b.next === id ? { ...b, next: undefined } : b));
    nodes[nid] = c;
  }
  const ids = Object.keys(nodes);
  return { ...flow, nodes, start: flow.start === id ? (ids[0] ?? "") : flow.start };
}

// renameNode changes a node's id and every link to it.
export function renameNode(flow: BotFlow, from: string, to: string): BotFlow {
  const swap = (v?: string) => (v === from ? to : v);
  const nodes: Record<string, BotNode> = {};
  for (const [nid, n] of Object.entries(flow.nodes)) {
    const c: BotNode = { ...n };
    if (c.next) c.next = swap(c.next);
    if (c.then) c.then = swap(c.then);
    if (c.else) c.else = swap(c.else);
    if (c.buttons) c.buttons = c.buttons.map((b) => ({ ...b, next: swap(b.next) }));
    nodes[nid === from ? to : nid] = c;
  }
  return { ...flow, nodes, start: flow.start === from ? to : flow.start };
}

export interface Placed {
  id: string;
  col: number;
  row: number;
  reachable: boolean;
}

// layout puts the start node in the first column and each node one column after the first node
// that links to it; nodes nothing reaches go in a last column.
export function layout(flow: BotFlow): Placed[] {
  const depth = new Map<string, number>();
  if (flow.start in flow.nodes) {
    depth.set(flow.start, 0);
    const queue = [flow.start];
    while (queue.length) {
      const id = queue.shift()!;
      for (const e of edges(flow.nodes[id])) {
        if (e.target in flow.nodes && !depth.has(e.target)) {
          depth.set(e.target, depth.get(id)! + 1);
          queue.push(e.target);
        }
      }
    }
  }
  const lost = Math.max(-1, ...depth.values()) + 1;
  const rows = new Map<number, number>();
  const out: Placed[] = [];
  const ids = Object.keys(flow.nodes).sort((a, b) => (depth.get(a) ?? lost) - (depth.get(b) ?? lost));
  for (const id of ids) {
    const col = depth.get(id) ?? lost;
    const row = rows.get(col) ?? 0;
    rows.set(col, row + 1);
    out.push({ id, col, row, reachable: depth.has(id) });
  }
  return out;
}

const VAR_RE = /^[A-Za-z0-9_]{1,40}$/;

export interface Problem {
  node?: string;
  message: string;
}

// problems lists what the api would refuse, so the builder can show it before saving.
export function problems(flow: BotFlow): Problem[] {
  const out: Problem[] = [];
  const ids = Object.keys(flow.nodes);
  if (ids.length === 0) return [{ message: "A flow needs at least one node." }];
  if (ids.length > MAX_NODES) out.push({ message: `A flow can have at most ${MAX_NODES} nodes.` });
  if (!(flow.start in flow.nodes)) out.push({ message: "Choose the first node." });
  flow.triggers.forEach((t, i) => {
    if (t.type === "keyword" && !(t.keywords ?? []).some((k) => k.trim())) out.push({ message: `Trigger ${i + 1} needs a keyword.` });
    if (t.type === "button_reply" && !t.button_id?.trim()) out.push({ message: `Trigger ${i + 1} needs a button id.` });
  });
  const link = (id: string, label: string, target?: string, required = false) => {
    if (!target) {
      if (required) out.push({ node: id, message: `Choose where "${label}" goes.` });
    } else if (!(target in flow.nodes)) {
      out.push({ node: id, message: `"${label}" points to a node that does not exist.` });
    }
  };
  for (const id of ids) {
    const n = flow.nodes[id];
    const text = (limit: number) => {
      const len = [...(n.text ?? "")].length;
      if (!(n.text ?? "").trim() || len > limit) out.push({ node: id, message: `The text needs 1 to ${limit} characters.` });
    };
    switch (n.type) {
      case "message":
        text(MAX_TEXT);
        link(id, "next", n.next);
        break;
      case "buttons": {
        text(MAX_BODY);
        const bs = n.buttons ?? [];
        if (bs.length < 1 || bs.length > MAX_BUTTONS) out.push({ node: id, message: `Use 1 to ${MAX_BUTTONS} buttons.` });
        const seen = new Set<string>();
        for (const b of bs) {
          const len = [...b.title].length;
          if (!b.title.trim() || len > MAX_BUTTON_TITLE) out.push({ node: id, message: `A button title needs 1 to ${MAX_BUTTON_TITLE} characters.` });
          if (!b.id.trim() || seen.has(b.id)) out.push({ node: id, message: "Every button needs its own id." });
          seen.add(b.id);
          link(id, b.title, b.next);
        }
        break;
      }
      case "question":
        text(MAX_TEXT);
        if (!VAR_RE.test(n.var ?? "")) out.push({ node: id, message: "Name the answer with letters, digits and underscores." });
        link(id, "next", n.next);
        break;
      case "condition":
        if (!VAR_RE.test(n.var ?? "")) out.push({ node: id, message: "Name the answer to check." });
        link(id, "then", n.then, true);
        link(id, "else", n.else);
        break;
      case "set":
        if (!VAR_RE.test(n.var ?? "")) out.push({ node: id, message: "Name the answer to set." });
        link(id, "next", n.next);
        break;
      case "tag":
        if (!(n.tag ?? "").trim()) out.push({ node: id, message: "Enter a tag." });
        link(id, "next", n.next);
        break;
      case "template":
        if (!n.template?.name || !n.template.language) out.push({ node: id, message: "Choose a template." });
        link(id, "next", n.next);
        break;
      case "handoff":
      case "end":
        if (n.text && [...n.text].length > MAX_TEXT) out.push({ node: id, message: `The text can have at most ${MAX_TEXT} characters.` });
        break;
    }
  }
  if (flow.start in flow.nodes && loops(flow)) out.push({ message: "The flow loops without waiting for the customer. Put a question or buttons in the loop." });
  return out;
}

const waits = (n: BotNode) => n.type === "buttons" || n.type === "question";

// loops reports a cycle made only of nodes that do not wait for the customer.
function loops(flow: BotFlow): boolean {
  const state = new Map<string, 1 | 2>();
  const visit = (id: string): boolean => {
    const s = state.get(id);
    if (s === 1) return true;
    if (s === 2) return false;
    state.set(id, 1);
    const n = flow.nodes[id];
    if (n && !waits(n)) {
      for (const e of edges(n)) if (e.target in flow.nodes && visit(e.target)) return true;
    }
    state.set(id, 2);
    return false;
  };
  return Object.keys(flow.nodes).some(visit);
}

// summary is a one-line description of a node for the flow map.
export function summary(n: BotNode): string {
  switch (n.type) {
    case "message":
    case "question":
    case "buttons":
    case "handoff":
    case "end":
      return n.text ?? "";
    case "condition":
      return `${n.var} ${n.op} ${n.value ?? ""}`.trim();
    case "set":
      return `${n.var} = ${n.value ?? ""}`;
    case "tag":
      return n.tag ?? "";
    case "template":
      return n.template?.name ?? "";
  }
}
