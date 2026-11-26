import { Badge, type BadgeProps } from "@/components/ui/badge";
import type { CameraState, IncidentStatus, Role, Severity } from "@/lib/types";
import { cn } from "@/lib/utils";

const SEVERITY_VARIANT: Record<Severity, BadgeProps["variant"]> = {
  low: "info",
  medium: "warning",
  high: "danger",
  critical: "danger",
};

export function SeverityBadge({ severity, className }: { severity: Severity; className?: string }) {
  return (
    <Badge
      variant={SEVERITY_VARIANT[severity] ?? "muted"}
      data-severity={severity}
      className={cn("capitalize", severity === "critical" && "bg-red-600 text-white dark:bg-red-600 dark:text-white", className)}
    >
      {severity}
    </Badge>
  );
}

const STATUS_VARIANT: Record<IncidentStatus, BadgeProps["variant"]> = {
  open: "danger",
  acknowledged: "warning",
  resolved: "success",
};

export function IncidentStatusBadge({ status }: { status: IncidentStatus }) {
  return (
    <Badge variant={STATUS_VARIANT[status] ?? "muted"} className="capitalize">
      {status}
    </Badge>
  );
}

const CAMERA_VARIANT: Record<CameraState, BadgeProps["variant"]> = {
  online: "success",
  starting: "info",
  offline: "muted",
  error: "danger",
  disabled: "muted",
};

export function CameraStateBadge({ state, fps }: { state: CameraState; fps?: number }) {
  return (
    <Badge variant={CAMERA_VARIANT[state] ?? "muted"} className="capitalize">
      <span
        aria-hidden
        className={cn(
          "size-1.5 rounded-full",
          state === "online" ? "animate-pulse bg-emerald-500" : state === "error" ? "bg-red-500" : state === "starting" ? "bg-sky-500" : "bg-muted-foreground",
        )}
      />
      {state}
      {state === "online" && typeof fps === "number" ? <span className="font-mono font-normal">· {fps.toFixed(1)} fps</span> : null}
    </Badge>
  );
}

const ROLE_VARIANT: Record<Role, BadgeProps["variant"]> = { admin: "default", operator: "info", viewer: "muted" };

export function RoleBadge({ role }: { role: Role }) {
  return (
    <Badge variant={ROLE_VARIANT[role]} className="capitalize">
      {role}
    </Badge>
  );
}
