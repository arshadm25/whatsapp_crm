import { describe, expect, it } from "vitest";
import { campaignSupported, fieldToken, previewBody, templateSlots } from "./campaigns";

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
  });

  it("previews the body", () => {
    expect(fieldToken("first_name", "there")).toBe("{{contact.first_name|there}}");
    expect(previewBody(comps, { "1": "{{contact.first_name|there}}", "2": "20%" })).toBe(
      'Hi [first_name or "there"], 20% off. [first_name or "there"]!',
    );
    expect(previewBody(comps, { "1": "{{ contact.city }}" })).toBe("Hi [city], {{2}} off. [city]!");
  });
});
