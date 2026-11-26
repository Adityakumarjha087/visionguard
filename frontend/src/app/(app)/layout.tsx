"use client";
import { useEffect } from "react";
import { usePathname, useRouter } from "next/navigation";
import { useAuth } from "@/components/auth/auth-provider";
import { AppShell } from "@/components/layout/app-shell";
import { Skeleton } from "@/components/ui/skeleton";

export default function AppLayout({ children }: { children: React.ReactNode }) {
  const { user, ready } = useAuth();
  const router = useRouter();
  const pathname = usePathname();

  useEffect(() => {
    if (ready && !user) router.replace(`/login?next=${encodeURIComponent(pathname)}`);
  }, [ready, user, router, pathname]);

  if (!ready || !user) {
    return (
      <div className="flex min-h-dvh items-center justify-center" aria-busy="true">
        <Skeleton className="h-8 w-40" />
      </div>
    );
  }
  return <AppShell>{children}</AppShell>;
}
