import type { Role } from "../api/types";

const ROLES: Role[] = ["owner", "admin", "agent", "developer"];

// Roles an actor may hand out or take away (same rule as internal/auth/team.go): owners
// manage everyone, admins manage agents and developers.
export function assignable(actor: Role | undefined): Role[] {
  if (actor === "owner") return ROLES;
  if (actor === "admin") return ["agent", "developer"];
  return [];
}
