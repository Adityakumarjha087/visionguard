"use client";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { ScanEye } from "lucide-react";
import { useAuth } from "@/components/auth/auth-provider";
import { cn } from "@/lib/utils";
import { NAV } from "./nav";

export function Brand() {
  return (
    <Link href="/dashboard" className="flex items-center gap-2 px-2 font-semibold tracking-tight">
      <span className="grid size-8 place-items-center rounded-lg bg-primary text-primary-foreground">
        <ScanEye className="size-5" />
      </span>
      VisionGuard
    </Link>
  );
}

export function SidebarNav({ onNavigate }: { onNavigate?: () => void }) {
  const pathname = usePathname();
  const { can } = useAuth();
  return (
    <nav aria-label="Main" className="grid gap-1">
      {NAV.filter((n) => !n.minRole || can(n.minRole)).map((n) => {
        const active = pathname === n.href || pathname.startsWith(`${n.href}/`);
        return (
          <Link
            key={n.href}
            href={n.href}
            onClick={onNavigate}
            aria-current={active ? "page" : undefined}
            className={cn(
              "flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium transition-colors",
              active ? "bg-primary/10 text-primary" : "text-muted-foreground hover:bg-accent hover:text-foreground",
            )}
          >
            <n.icon className="size-4" />
            {n.label}
          </Link>
        );
      })}
    </nav>
  );
}

export function Sidebar() {
  return (
    <aside className="sticky top-0 hidden h-dvh w-60 shrink-0 flex-col gap-6 border-r bg-sidebar p-4 lg:flex">
      <Brand />
      <SidebarNav />
      <p className="mt-auto px-2 text-xs text-muted-foreground">Real-time CV monitoring</p>
    </aside>
  );
}
