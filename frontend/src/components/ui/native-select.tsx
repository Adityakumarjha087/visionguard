import * as React from "react";
import { ChevronDown } from "lucide-react";
import { cn } from "@/lib/utils";

/** shadcn-styled native <select>: fully accessible, works on mobile and in jsdom. */
export const NativeSelect = React.forwardRef<HTMLSelectElement, React.SelectHTMLAttributes<HTMLSelectElement>>(
  ({ className, children, ...props }, ref) => (
    <div className={cn("relative", className)}>
      <select
        ref={ref}
        className="flex h-9 w-full appearance-none rounded-md border border-input bg-background py-1 pl-3 pr-8 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50"
        {...props}
      >
        {children}
      </select>
      <ChevronDown aria-hidden className="pointer-events-none absolute right-2.5 top-2.5 size-4 text-muted-foreground" />
    </div>
  ),
);
NativeSelect.displayName = "NativeSelect";
