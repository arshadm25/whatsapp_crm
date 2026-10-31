import { describe, expect, it } from "vitest";
import { campaignSupported, estimateSend, fieldToken, previewBody, previewText, templateSlots } from "./campaigns";
import { dailyLimit } from "./numbers";

describe("campaign helpers", () => {
  const comps = [
    { type: "HEADER" as const, format: "IMAGE" },
    { type: "BODY" as const, text: "Hi {{1}}, {{2}} off. {{1}}!" },
    { type: "BUTTONS" as const, buttons: [{ type: "QUICK_REPLY", text: "Stop" }, { type: "URL", text: "Shop", url: "https://x.in/{{1}}" }] },
  ];

  it("lists the values a template needs", () => {
    expect(templateSlots(comps)).toEqual([
      { key: "header", part: "header", media: "image" },
      { key: "1", part: "body" },
      { key: "2", part: "body" },
      { key: "button.1", part: "button" },
    ]);
    expect(templateSlots([{ type: "HEADER", format: "TEXT", text: "Order {{num}}" }])).toEqual([{ key: "header.num", part: "header" }]);
  });

  it("refuses location headers", () => {
    expect(campaignSupported(comps)).toBe(true);
    expect(campaignSupported([{ type: "HEADER", format: "LOCATION" }])).toBe(false);
    expect(campaignSupported([{ type: "BODY", text: "x" }, { type: "CAROUSEL" }])).toBe(false);
  });

  it("previews the body", () => {
    expect(fieldToken("first_name", "there")).toBe("{{contact.first_name|there}}");
    expect(previewBody(comps, { "1": "{{contact.first_name|there}}", "2": "20%" })).toBe(
      'Hi [first_name or "there"], 20% off. [first_name or "there"]!',
    );
    expect(previewBody(comps, { "1": "{{ contact.city }}" })).toBe("Hi [city], {{2}} off. [city]!");
  });

  it("previews a text header", () => {
    expect(previewText("Order {{1}}", { "header.1": "A12" }, "header.")).toBe("Order A12");
    expect(previewText("Order {{1}}", {}, "header.")).toBe("Order {{1}}");
  });

  it("estimates how long sending takes", () => {
    expect(estimateSend(0, 1000, 250)).toBeNull();
    expect(estimateSend(120, 1000, 250)).toEqual({ kind: "minutes", value: 1 });
    expect(estimateSend(2500, 1000, -1)).toEqual({ kind: "minutes", value: 3 });
    expect(estimateSend(600, 100, 250)).toEqual({ kind: "days", value: 3 });
    expect(estimateSend(250, 10, 250)).toEqual({ kind: "minutes", value: 25 });
  });

  it("reads the daily limit like the server", () => {
    expect(dailyLimit(null)).toBe(250);
    expect(dailyLimit("TIER_1K")).toBe(1000);
    expect(dailyLimit("TIER_UNLIMITED")).toBe(-1);
    expect(dailyLimit("SOMETHING_NEW")).toBe(250);
  });
});
