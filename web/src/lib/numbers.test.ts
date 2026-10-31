import { describe, expect, it } from "vitest";
import { registrationState, usageLevel, usagePct } from "./numbers";

describe("registrationState", () => {
  it("is registered when Meta registration finished", () => {
    expect(registrationState({ status: "connected", is_coexistence: false, registered_at: "2026-10-24T20:52:00Z" })).toBe("registered");
  });
  it("treats a Business app number as registered to the app, not missing", () => {
    expect(registrationState({ status: "connected", is_coexistence: true, registered_at: null })).toBe("businessApp");
  });
  it("flags a Cloud API number that never registered", () => {
    expect(registrationState({ status: "connected", is_coexistence: false })).toBe("notRegistered");
    expect(registrationState({ status: "pending", is_coexistence: false, registered_at: null })).toBe("notRegistered");
  });
  it("says nothing for numbers that are not in use", () => {
    expect(registrationState({ status: "revoked", is_coexistence: false })).toBe("none");
    expect(registrationState({ status: "error", is_coexistence: false })).toBe("none");
  });
});

describe("usage", () => {
  it("rates how full the daily limit is", () => {
    expect(usageLevel(0, 250)).toBe("ok");
    expect(usageLevel(199, 250)).toBe("ok");
    expect(usageLevel(200, 250)).toBe("near");
    expect(usageLevel(250, 250)).toBe("full");
    expect(usageLevel(5000, -1)).toBe("ok");
  });
  it("gives a capped percentage", () => {
    expect(usagePct(125, 250)).toBe(50);
    expect(usagePct(300, 250)).toBe(100);
    expect(usagePct(10, -1)).toBe(0);
  });
});
