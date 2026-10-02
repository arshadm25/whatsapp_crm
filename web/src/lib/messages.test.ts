import { describe, expect, it } from "vitest";
import { messageText } from "./messages";

describe("messageText", () => {
  it("reads each message type", () => {
    expect(messageText({ type: "text", content: { type: "text", text: { body: "नमस्ते" } } })).toBe("नमस्ते");
    expect(messageText({ type: "image", content: { image: { caption: "New stock" } } })).toBe("[image] New stock");
    expect(messageText({ type: "template", content: { template: { name: "order_shipped" } } })).toBe("Template: order_shipped");
    expect(messageText({ type: "interactive", content: { interactive: { button_reply: { title: "Yes" } } } })).toBe("Yes");
    expect(messageText({ type: "unsupported", content: {} })).toBe("[unsupported]");
  });
});
