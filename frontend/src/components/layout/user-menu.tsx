"use client";
import { useRouter } from "next/navigation";
import { LogOut, UserRound } from "lucide-react";
import { useAuth } from "@/components/auth/auth-provider";
import { RoleBadge } from "@/components/common/badges";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

export function UserMenu() {
  const { user, logout } = useAuth();
  const router = useRouter();
  if (!user) return null;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" className="gap-2 px-2" aria-label="User menu">
          <span className="grid size-7 place-items-center rounded-full bg-primary/15 text-xs font-semibold uppercase text-primary">
            {user.name?.[0] ?? user.email[0]}
          </span>
          <span className="hidden max-w-32 truncate text-sm md:inline">{user.name || user.email}</span>
          <RoleBadge role={user.role} />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-56">
        <DropdownMenuLabel className="font-normal">
          <div className="flex items-center gap-2">
            <UserRound className="size-4" />
            <div className="min-w-0">
              <p className="truncate text-sm font-medium">{user.name}</p>
              <p className="truncate text-xs text-muted-foreground">{user.email}</p>
            </div>
          </div>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem
          onSelect={async () => {
            await logout();
            router.replace("/login");
          }}
        >
          <LogOut /> Log out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
