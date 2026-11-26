"use client";
import { useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";
import { AuthCard } from "@/components/auth/auth-card";
import { useAuth } from "@/components/auth/auth-provider";
import { validateCredentials } from "@/components/auth/login-form";
import { Field } from "@/components/common/field";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { errorMessage } from "@/lib/api";

export default function RegisterPage() {
  const { register } = useAuth();
  const router = useRouter();
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [pending, setPending] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    const v = validateCredentials(email, password, 8);
    if (!name.trim()) v.name = "Name is required";
    setErrors(v);
    setFormError(null);
    if (Object.keys(v).length) return;
    setPending(true);
    try {
      await register(email.trim(), password, name.trim());
      toast.success("Account created (viewer role)");
      router.replace("/dashboard");
    } catch (err) {
      setFormError(errorMessage(err));
    } finally {
      setPending(false);
    }
  }

  return (
    <AuthCard title="Create an account" subtitle="New accounts start with the viewer role">
      <form onSubmit={submit} noValidate className="grid gap-4" aria-label="Register">
        <Field id="name" label="Name" error={errors.name}>
          <Input id="name" autoComplete="name" value={name} onChange={(e) => setName(e.target.value)} aria-invalid={!!errors.name} />
        </Field>
        <Field id="email" label="Email" error={errors.email}>
          <Input id="email" type="email" autoComplete="email" value={email} onChange={(e) => setEmail(e.target.value)} aria-invalid={!!errors.email} />
        </Field>
        <Field id="password" label="Password" error={errors.password} hint="At least 8 characters">
          <Input
            id="password"
            type="password"
            autoComplete="new-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            aria-invalid={!!errors.password}
          />
        </Field>
        {formError ? (
          <p role="alert" className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
            {formError}
          </p>
        ) : null}
        <Button type="submit" disabled={pending}>
          {pending ? <Loader2 className="animate-spin" /> : null}
          Create account
        </Button>
      </form>
      <p className="mt-6 text-center text-sm text-muted-foreground">
        Already registered?{" "}
        <Link href="/login" className="font-medium text-primary underline-offset-4 hover:underline">
          Sign in
        </Link>
      </p>
    </AuthCard>
  );
}
