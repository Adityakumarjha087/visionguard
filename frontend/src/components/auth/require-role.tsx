"use client";
import type { Role } from "@/lib/types";
import { useAuth } from "./auth-provider";

/** Renders children only when the current user has at least `role`. */
export function RequireRole({ role, children, fallback = null }: { role: Role; children: React.ReactNode; fallback?: React.ReactNode }) {
  const { can } = useAuth();
  return <>{can(role) ? children : fallback}</>;
}
