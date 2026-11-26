"use client";
import { useState } from "react";
import { Menu } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetDescription, SheetTitle } from "@/components/ui/sheet";
import { ConnectionIndicator } from "./connection-indicator";
import { Brand, Sidebar, SidebarNav } from "./sidebar";
import { ThemeToggle } from "./theme-toggle";
import { UserMenu } from "./user-menu";
import { AlertToaster } from "./alert-toaster";

export function AppShell({ children }: { children: React.ReactNode }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="flex min-h-dvh">
      <Sidebar />
      <Sheet open={open} onOpenChange={setOpen}>
        <SheetContent side="left" className="p-4">
          <SheetTitle className="sr-only">Navigation</SheetTitle>
          <SheetDescription className="sr-only">Main navigation</SheetDescription>
          <Brand />
          <SidebarNav onNavigate={() => setOpen(false)} />
        </SheetContent>
      </Sheet>
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="sticky top-0 z-30 flex h-14 items-center gap-2 border-b bg-background/80 px-4 backdrop-blur supports-[backdrop-filter]:bg-background/60">
          <Button variant="ghost" size="icon" className="lg:hidden" aria-label="Open navigation" onClick={() => setOpen(true)}>
            <Menu />
          </Button>
          <div className="lg:hidden">
            <Brand />
          </div>
          <div className="ml-auto flex items-center gap-1 sm:gap-2">
            <ConnectionIndicator />
            <ThemeToggle />
            <UserMenu />
          </div>
        </header>
        <main id="main" className="mx-auto w-full max-w-[1600px] flex-1 space-y-6 p-4 md:p-6">
          {children}
        </main>
      </div>
      <AlertToaster />
    </div>
  );
}
