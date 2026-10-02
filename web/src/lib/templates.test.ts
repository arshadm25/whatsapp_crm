import { describe, expect, it } from "vitest";
import { bodyVariables, buildComponents, placeholders, toTemplateName } from "./templates";

describe("template helpers", () => {
  it("finds distinct variables in order", () => {
    expect(placeholders("Hi {{1}}, order {{2}} for {{ 1 }}")).toEqual(["1", "2"]);
  });

  it("builds Meta components with examples", () => {
    const comps = buildComponents({
      header: "Order {{1}}",
      body: "आपका ऑर्डर {{1}} भेज दिया गया है।",
      footer: "Sharma Sweets",
      quickReplies: ["Track", " "],
      samples: { "h:1": "ORD-1", "b:1": "ORD-4821" },
    });
    expect(comps).toEqual([
      { type: "HEADER", format: "TEXT", text: "Order {{1}}", example: { header_text: ["ORD-1"] } },
      { type: "BODY", text: "आपका ऑर्डर {{1}} भेज दिया गया है।", example: { body_text: [["ORD-4821"]] } },
      { type: "FOOTER", text: "Sharma Sweets" },
      { type: "BUTTONS", buttons: [{ type: "QUICK_REPLY", text: "Track" }] },
    ]);
    expect(bodyVariables(comps)).toEqual(["1"]);
  });

  it("builds a media header from an uploaded sample", () => {
    const comps = buildComponents({
      header: "ignored", headerMedia: { format: "IMAGE", handle: "4::abc" }, body: "New stock", footer: "", quickReplies: [], samples: {},
    });
    expect(comps[0]).toEqual({ type: "HEADER", format: "IMAGE", example: { header_handle: ["4::abc"] } });
  });

  it("normalises names", () => {
    expect(toTemplateName("Order Shipped!")).toBe("order_shipped_");
  });
});
