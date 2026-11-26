"use client";
import { useState } from "react";
import { Loader2 } from "lucide-react";
import { Field } from "@/components/common/field";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { errorMessage } from "@/lib/api";

export const DEMO_ACCOUNTS = [
  { role: "admin", email: "admin@visionguard.dev", password: "admin12345" },
  { role: "operator", email: "operator@visionguard.dev", password: "operator12345" },
  { role: "viewer", email: "viewer@visionguard.dev", password: "viewer12345" },
] as const;

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

export function validateCredentials(email: string, password: string, minPassword = 1): Record<string, string> {
  const errors: Record<string, string> = {};
  if (!email.trim()) errors.email = "Email is required";
  else if (!EMAIL_RE.test(email.trim())) errors.email = "Enter a valid email address";
  if (!password) errors.password = "Password is required";
  else if (password.length < minPassword) errors.password = `Password must be at least ${minPassword} characters`;
  return errors;
}

export function LoginForm({ onSubmit }: { onSubmit: (email: string, password: string) => Promise<void> }) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [pending, setPending] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    const v = validateCredentials(email, password);
    setErrors(v);
    setFormError(null);
    if (Object.keys(v).length) return;
    setPending(true);
    try {
      await onSubmit(email.trim(), password);
    } catch (err) {
      setFormError(errorMessage(err));
    } finally {
      setPending(false);
    }
  }

  return (
    <div className="space-y-6">
      <form onSubmit={submit} noValidate className="grid gap-4" aria-label="Sign in">
        <Field id="email" label="Email" error={errors.email}>
          <Input
            id="email"
            type="email"
            autoComplete="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            aria-invalid={!!errors.email}
            aria-describedby={errors.email ? "email-error" : undefined}
          />
        </Field>
        <Field id="password" label="Password" error={errors.password}>
          <Input
            id="password"
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            aria-invalid={!!errors.password}
            aria-describedby={errors.password ? "password-error" : undefined}
          />
        </Field>
        {formError ? (
          <p role="alert" className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
            {formError}
          </p>
        ) : null}
        <Button type="submit" disabled={pending} className="w-full">
          {pending ? <Loader2 className="animate-spin" /> : null}
          Sign in
        </Button>
      </form>
      <div className="rounded-lg border bg-muted/40 p-3 text-xs">
        <p className="mb-2 font-medium text-foreground">Demo accounts (click to fill)</p>
        <ul className="grid gap-1">
          {DEMO_ACCOUNTS.map((a) => (
            <li key={a.email}>
              <button
                type="button"
                className="flex w-full cursor-pointer justify-between gap-2 rounded px-2 py-1 text-left font-mono text-muted-foreground hover:bg-accent hover:text-foreground"
                onClick={() => {
                  setEmail(a.email);
                  setPassword(a.password);
                  setErrors({});
                }}
                aria-label={`Use ${a.role} demo account`}
              >
                <span>{a.email}</span>
                <span>{a.password}</span>
              </button>
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}
