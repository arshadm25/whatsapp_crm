import { describe, expect, it } from "vitest";
import { daysLeft, formatPaise, graceEnd, rupeesToPaise } from "./billing";
import type { Subscription } from "../api/types";

describe("billing helpers", () => {
  it("formats paise as rupees", () => {
    expect(formatPaise(299900)).toBe("₹2,999");
    expect(formatPaise(49950)).toBe("₹499.5");
  });
  it("counts days left, rounding up", () => {
    const now = new Date("2026-10-02T10:00:00Z");
    expect(daysLeft("2026-10-16T10:00:00Z", now)).toBe(14);
    expect(daysLeft("2026-10-02T11:00:00Z", now)).toBe(1);
    expect(daysLeft("2026-10-01T10:00:00Z", now)).toBe(0);
  });
  it("adds the 7-day grace after a failed charge", () => {
    const sub = { current_period_end: "2026-10-02T00:00:00Z" } as Subscription;
    expect(graceEnd(sub).toISOString()).toBe("2026-10-09T00:00:00.000Z");
  });
  it("reads rupee amounts", () => {
    expect(rupeesToPaise("2,999")).toBe(299900);
    expect(rupeesToPaise("₹ 499.50")).toBe(49950);
    expect(rupeesToPaise("abc")).toBeNull();
    expect(rupeesToPaise("-1")).toBeNull();
  });
});
