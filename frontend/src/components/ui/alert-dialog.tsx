"use client";
import * as React from "react";
import { AlertDialog as AD } from "radix-ui";
import { buttonVariants } from "./button";
import { cn } from "@/lib/utils";

export interface ConfirmDialogProps {
  trigger: React.ReactNode;
  title: string;
  description?: string;
  confirmLabel?: string;
  onConfirm: () => void | Promise<void>;
}

/** shadcn-style AlertDialog wrapped as a single confirm component. */
export function ConfirmDialog({ trigger, title, description, confirmLabel = "Delete", onConfirm }: ConfirmDialogProps) {
  return (
    <AD.Root>
      <AD.Trigger asChild>{trigger}</AD.Trigger>
      <AD.Portal>
        <AD.Overlay className="fixed inset-0 z-50 bg-black/60" />
        <AD.Content className="fixed left-1/2 top-1/2 z-50 grid w-[calc(100%-2rem)] max-w-md -translate-x-1/2 -translate-y-1/2 gap-4 rounded-xl border bg-background p-6 shadow-lg">
          <AD.Title className="text-lg font-semibold">{title}</AD.Title>
          {description ? (
            <AD.Description className="text-sm text-muted-foreground">{description}</AD.Description>
          ) : (
            <AD.Description className="sr-only">Confirm action</AD.Description>
          )}
          <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
            <AD.Cancel className={buttonVariants({ variant: "outline" })}>Cancel</AD.Cancel>
            <AD.Action className={cn(buttonVariants({ variant: "destructive" }))} onClick={() => void onConfirm()}>
              {confirmLabel}
            </AD.Action>
          </div>
        </AD.Content>
      </AD.Portal>
    </AD.Root>
  );
}
