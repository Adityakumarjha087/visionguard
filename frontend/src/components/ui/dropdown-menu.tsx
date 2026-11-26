"use client";
import * as React from "react";
import { DropdownMenu as DM } from "radix-ui";
import { Check } from "lucide-react";
import { cn } from "@/lib/utils";

export const DropdownMenu = DM.Root;
export const DropdownMenuTrigger = DM.Trigger;
export const DropdownMenuRadioGroup = DM.RadioGroup;

export const DropdownMenuContent = React.forwardRef<
  React.ComponentRef<typeof DM.Content>,
  React.ComponentPropsWithoutRef<typeof DM.Content>
>(({ className, sideOffset = 6, ...props }, ref) => (
  <DM.Portal>
    <DM.Content
      ref={ref}
      sideOffset={sideOffset}
      className={cn(
        "z-50 min-w-40 overflow-hidden rounded-md border bg-popover p-1 text-popover-foreground shadow-md data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=open]:zoom-in-95",
        className,
      )}
      {...props}
    />
  </DM.Portal>
));
DropdownMenuContent.displayName = "DropdownMenuContent";

const itemCls =
  "relative flex cursor-pointer select-none items-center gap-2 rounded-sm px-2 py-1.5 text-sm outline-none transition-colors focus:bg-accent focus:text-accent-foreground data-[disabled]:pointer-events-none data-[disabled]:opacity-50 [&_svg]:size-4";

export const DropdownMenuItem = React.forwardRef<
  React.ComponentRef<typeof DM.Item>,
  React.ComponentPropsWithoutRef<typeof DM.Item>
>(({ className, ...props }, ref) => <DM.Item ref={ref} className={cn(itemCls, className)} {...props} />);
DropdownMenuItem.displayName = "DropdownMenuItem";

export const DropdownMenuRadioItem = React.forwardRef<
  React.ComponentRef<typeof DM.RadioItem>,
  React.ComponentPropsWithoutRef<typeof DM.RadioItem>
>(({ className, children, ...props }, ref) => (
  <DM.RadioItem ref={ref} className={cn(itemCls, "pl-8", className)} {...props}>
    <span className="absolute left-2 flex size-4 items-center justify-center">
      <DM.ItemIndicator>
        <Check />
      </DM.ItemIndicator>
    </span>
    {children}
  </DM.RadioItem>
));
DropdownMenuRadioItem.displayName = "DropdownMenuRadioItem";

export function DropdownMenuLabel({ className, ...props }: React.ComponentPropsWithoutRef<typeof DM.Label>) {
  return <DM.Label className={cn("px-2 py-1.5 text-sm font-semibold", className)} {...props} />;
}
export function DropdownMenuSeparator({ className, ...props }: React.ComponentPropsWithoutRef<typeof DM.Separator>) {
  return <DM.Separator className={cn("-mx-1 my-1 h-px bg-muted", className)} {...props} />;
}
