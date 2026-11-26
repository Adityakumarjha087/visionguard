"use client";
import { usePathname, useRouter } from "next/navigation";
import { toast } from "sonner";
import { useWsEvent } from "@/components/realtime-provider";

/** Global toast for every WS `alert` (the dashboard additionally shows a feed). */
export function AlertToaster() {
  const router = useRouter();
  const pathname = usePathname();
  useWsEvent("alert", ({ data }) => {
    const fn = data.severity === "critical" || data.severity === "high" ? toast.error : toast.warning;
    fn(`${data.rule_name || "Alert"} · ${data.camera_name}`, {
      id: `alert-${data.id}`,
      description: data.message,
      action: pathname.startsWith("/incidents")
        ? undefined
        : { label: "View", onClick: () => router.push(`/incidents?id=${data.id}`) },
    });
  });
  return null;
}
