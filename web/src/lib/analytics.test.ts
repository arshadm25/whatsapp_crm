import { describe, expect, it } from "vitest";
import { daysAgo, duration, percent, reportCSV, rupees, scale } from "./analytics";

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

describe("duration and CSV", () => {
  it("formats waits", () => {
    expect(duration(null)).toBe("—");
    expect(duration(42)).toBe("42s");
    expect(duration(200)).toBe("3m 20s");
    expect(duration(7500)).toBe("2h 5m");
  });
  it("exports days", () => {
    expect(reportCSV([{ day: "2026-10-01", sent: 2, delivered: 2, read: 1, failed: 0, received: 1, billable: 1, est_cost_minor: 78 }])).toBe(
      "date,sent,delivered,read,failed,received,billable,estimated_cost_inr\n2026-10-01,2,2,1,0,1,1,0.78\n",
    );
  });
});
