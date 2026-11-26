"use client";
import { ThemeProvider } from "next-themes";
import { Toaster } from "sonner";
import { AuthProvider } from "./auth/auth-provider";
import { RealtimeProvider } from "./realtime-provider";

export function Providers({ children }: { children: React.ReactNode }) {
  return (
    <ThemeProvider attribute="class" defaultTheme="system" enableSystem disableTransitionOnChange>
      <AuthProvider>
        <RealtimeProvider>{children}</RealtimeProvider>
      </AuthProvider>
      <Toaster richColors closeButton position="top-right" />
    </ThemeProvider>
  );
}
