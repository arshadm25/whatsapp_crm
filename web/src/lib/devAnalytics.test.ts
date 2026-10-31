import { describe, expect, it } from "vitest";
import { buckets, fill, ms, pct, statusTone } from "./devAnalytics";

describe("developer chart buckets", () => {
  it("lays out one bucket per day in the workspace time zone and fills gaps with zero", () => {
    // 25 Oct 00:00 in Kolkata is 24 Oct 18:30 UTC.
    const meta = { from: "2026-10-24T18:30:00Z", hourly: false, time_zone: "Asia/Kolkata", days: 7 };
    const list = buckets(meta, new Date("2026-10-31T10:00:00Z"));
    expect(list.map((b) => b.key)).toEqual(["2026-10-25", "2026-10-26", "2026-10-27", "2026-10-28", "2026-10-29", "2026-10-30", "2026-10-31"]);
    const points = [
      { bucket: "2026-10-25T18:30:00Z", n: 3 }, // 26 Oct
      { bucket: "2026-10-25T18:30:00Z", n: 2 },
      { bucket: "2026-10-30T18:30:00Z", n: 5 }, // 31 Oct
    ];
    expect(fill(meta, list, points, (p) => p.n)).toEqual([0, 5, 0, 0, 0, 0, 5]);
  });

  it("uses hours for a one-day range", () => {
    const meta = { from: "2026-10-30T10:00:00Z", hourly: true, time_zone: "Asia/Kolkata", days: 1 };
    const list = buckets(meta, new Date("2026-10-31T10:20:00Z"));
    expect(list).toHaveLength(25);
    expect(fill(meta, list, [{ bucket: "2026-10-31T10:00:00Z", n: 4 }], (p) => p.n).at(-1)).toBe(4);
  });
});

describe("formatting", () => {
  it("formats rates, latency and status tones", () => {
    expect(pct(1, 3)).toBe("33%");
    expect(pct(1, 200)).toBe("0.5%");
    expect(pct(0, 0)).toBe("—");
    expect(ms(120.4)).toBe("120 ms");
    expect(ms(2400)).toBe("2.4 s");
    expect([statusTone(200), statusTone(404), statusTone(502)]).toEqual(["ok", "wa", "er"]);
  });
});
