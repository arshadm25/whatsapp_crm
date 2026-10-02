import { describe, expect, it } from "vitest";
import { daysAgo, percent, rupees, scale } from "./analytics";

describe("analytics helpers", () => {
  it("formats money and shares", () => {
    expect(rupees(12345)).toBe("₹123.45");
    expect(percent(1, 3)).toBe("33%");
    expect(percent(0, 0)).toBe("—");
  });

  it("computes dates and bar scales", () => {
    expect(daysAgo(0, new Date(2026, 9, 2))).toBe("2026-10-02");
    expect(daysAgo(29, new Date(2026, 9, 2))).toBe("2026-09-03");
    expect(scale([0, 5, 10])).toEqual([0, 0.5, 1]);
    expect(scale([0, 0])).toEqual([0, 0]);
  });
});
