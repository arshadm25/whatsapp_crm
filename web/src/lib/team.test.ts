import { describe, expect, it } from "vitest";
import { assignable } from "./team";

describe("assignable roles", () => {
  it("follows the server's rule", () => {
    expect(assignable("owner")).toEqual(["owner", "admin", "agent", "developer"]);
    expect(assignable("admin")).toEqual(["agent", "developer"]);
    expect(assignable("agent")).toEqual([]);
    expect(assignable(undefined)).toEqual([]);
  });
});
