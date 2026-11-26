import { ScanEye } from "lucide-react";
import { ThemeToggle } from "@/components/layout/theme-toggle";

export function AuthCard({ title, subtitle, children }: { title: string; subtitle: string; children: React.ReactNode }) {
  return (
    <main className="relative grid min-h-dvh place-items-center bg-gradient-to-br from-primary/5 via-background to-background p-4">
      <div className="absolute right-4 top-4">
        <ThemeToggle />
      </div>
      <div className="w-full max-w-md rounded-2xl border bg-card p-6 shadow-xl sm:p-8">
        <div className="mb-6 flex items-center gap-3">
          <span className="grid size-10 place-items-center rounded-xl bg-primary text-primary-foreground">
            <ScanEye className="size-6" />
          </span>
          <div>
            <h1 className="text-xl font-bold tracking-tight">{title}</h1>
            <p className="text-sm text-muted-foreground">{subtitle}</p>
          </div>
        </div>
        {children}
      </div>
    </main>
  );
}
