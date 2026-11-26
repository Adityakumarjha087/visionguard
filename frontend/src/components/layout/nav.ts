import { BarChart3, Boxes, Camera, LayoutDashboard, ShieldAlert, Users } from "lucide-react";
import type { Role } from "@/lib/types";

export interface NavItem {
  href: string;
  label: string;
  icon: React.ComponentType<{ className?: string }>;
  minRole?: Role;
}

export const NAV: NavItem[] = [
  { href: "/dashboard", label: "Dashboard", icon: LayoutDashboard },
  { href: "/cameras", label: "Cameras", icon: Camera },
  { href: "/incidents", label: "Incidents", icon: ShieldAlert },
  { href: "/analytics", label: "Analytics", icon: BarChart3 },
  { href: "/models", label: "Models", icon: Boxes },
  { href: "/users", label: "Users", icon: Users, minRole: "admin" },
];
