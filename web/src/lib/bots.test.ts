import { describe, expect, it } from "vitest";
import { emptyFlow, layout, newNode, nextId, problems, renameNode, withoutNode, type BotFlow } from "./bots";

const flow = (): BotFlow => ({
  start: "menu",
  triggers: [{ type: "keyword", keywords: ["hi"] }],
  nodes: {
    menu: { type: "buttons", text: "Pick", buttons: [{ id: "a", title: "A", next: "ask" }, { id: "b", title: "B", next: "bye" }] },
    ask: { type: "question", text: "Email?", var: "email", kind: "email", next: "bye" },
    bye: { type: "end", text: "Bye" },
    orphan: { type: "message", text: "Never sent" },
  },
});

describe("bot flows", () => {
  it("accepts the starter flow", () => {
    expect(problems(emptyFlow())).toEqual([]);
    expect(problems(flow())).toEqual([]);
  });

  it("lays nodes out by distance from the start and parks unreachable ones last", () => {
    const placed = Object.fromEntries(layout(flow()).map((p) => [p.id, p]));
    expect(placed.menu).toMatchObject({ col: 0, row: 0, reachable: true });
    expect(placed.ask.col).toBe(1);
    expect(placed.bye.col).toBe(1);
    expect(placed.bye.row).toBe(1);
    expect(placed.orphan).toMatchObject({ col: 2, reachable: false });
  });

  it("removes a node and the links to it", () => {
    const f = withoutNode(flow(), "bye");
    expect(f.nodes.ask.next).toBeUndefined();
    expect(f.nodes.menu.buttons![1].next).toBeUndefined();
    expect(withoutNode(flow(), "menu").start).not.toBe("menu");
  });

  it("renames a node and the links to it", () => {
    const f = renameNode(flow(), "ask", "email_step");
    expect(f.nodes.email_step).toBeDefined();
    expect(f.nodes.ask).toBeUndefined();
    expect(f.nodes.menu.buttons![0].next).toBe("email_step");
    expect(renameNode(flow(), "menu", "start").start).toBe("start");
  });

  it("picks unused ids", () => {
    const f = flow();
    f.nodes.message_1 = newNode("message");
    expect(nextId(f, "message")).toBe("message_2");
  });

  it("reports what the api would refuse", () => {
    const f = flow();
    f.nodes.menu.buttons = [...f.nodes.menu.buttons!, { id: "c", title: "x".repeat(21) }];
    f.nodes.ask.next = "gone";
    f.nodes.bye.text = "";
    const msgs = problems(f).map((p) => p.message);
    expect(msgs).toEqual(expect.arrayContaining([
      expect.stringContaining("button title"),
      expect.stringContaining("does not exist"),
    ]));
    expect(problems(f).some((p) => p.node === "bye" || p.node === "ask")).toBe(true);
  });

  it("checks a Flow step and lets it wait for the customer", () => {
    const f: BotFlow = { start: "form", triggers: [], nodes: { form: { type: "flow", text: "Fill in", flow_id: "", cta: "Open", next: "form" } } };
    expect(problems(f).map((p) => p.message)).toContain("Choose a Flow.");
    f.nodes.form.flow_id = "7d2c1f0e-3a55-4a39-9f0b-0f6f7f2f6a10";
    expect(problems(f)).toEqual([]);
  });

  it("checks an AI step, links its else path and lets it wait for the customer", () => {
    const f: BotFlow = {
      start: "ask",
      triggers: [],
      nodes: { ask: { ...newNode("ai"), next: "ask", else: "person" }, person: { type: "handoff", text: "", reason: "ai_unsure" } },
    };
    expect(problems(f)).toEqual([]);
    f.nodes.ask.threshold = 2;
    f.nodes.ask.else = "missing";
    expect(problems(f).length).toBe(2);
  });

  it("refuses a loop that never waits and allows one that does", () => {
    const loop: BotFlow = {
      start: "a",
      triggers: [],
      nodes: { a: { type: "message", text: "x", next: "b" }, b: { type: "message", text: "y", next: "a" } },
    };
    expect(problems(loop).map((p) => p.message).join()).toContain("loops");
    loop.nodes.b = { type: "question", text: "again?", var: "x", kind: "text", next: "a" };
    expect(problems(loop)).toEqual([]);
  });
});
