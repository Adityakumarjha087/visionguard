import type { Role } from "./types";

const RANK: Record<Role, number> = { viewer: 0, operator: 1, admin: 2 };

/** True when `role` is at least `min` (admin > operator > viewer). */
export function hasRole(role: Role | undefined | null, min: Role): boolean {
  if (!role) return false;
  return RANK[role] >= RANK[min];
}
