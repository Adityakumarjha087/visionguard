"use client";
import { Suspense, useEffect } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { toast } from "sonner";
import { AuthCard } from "@/components/auth/auth-card";
import { useAuth } from "@/components/auth/auth-provider";
import { LoginForm } from "@/components/auth/login-form";

function safeNext(next: string | null): string {
  return next && next.startsWith("/") && !next.startsWith("//") ? next : "/dashboard";
}

function LoginInner() {
  const { login, user, ready } = useAuth();
  const router = useRouter();
  const params = useSearchParams();
  const next = safeNext(params.get("next"));

  useEffect(() => {
    if (ready && user) router.replace(next);
  }, [ready, user, router, next]);

  return (
    <AuthCard title="Sign in to VisionGuard" subtitle="Real-time video analytics">
      <LoginForm
        onSubmit={async (email, password) => {
          const u = await login(email, password);
          toast.success(`Welcome back, ${u.name || u.email}`);
          router.replace(next);
        }}
      />
      <p className="mt-6 text-center text-sm text-muted-foreground">
        No account?{" "}
        <Link href="/register" className="font-medium text-primary underline-offset-4 hover:underline">
          Create one
        </Link>
      </p>
    </AuthCard>
  );
}

export default function LoginPage() {
  return (
    <Suspense>
      <LoginInner />
    </Suspense>
  );
}
