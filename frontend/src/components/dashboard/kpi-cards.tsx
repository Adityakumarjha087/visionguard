import { ArrowDownLeft, ArrowUpRight, Camera, Car, ShieldAlert, Users } from "lucide-react";
import { Card } from "@/components/ui/card";
import type { AnalyticsSummary } from "@/lib/types";

export function KpiCards({ s }: { s: AnalyticsSummary }) {
  const items = [
    { label: "Cameras online", value: `${s.cameras_online}/${s.cameras_total}`, icon: Camera, hint: "live sources" },
    { label: "Open incidents", value: s.open_incidents, icon: ShieldAlert, hint: `${s.incidents_24h} in last 24h` },
    { label: "People now", value: s.people_now, icon: Users, hint: "across all cameras" },
    { label: "Vehicles now", value: s.vehicles_now, icon: Car, hint: "across all cameras" },
    {
      label: "Line crossings 24h",
      value: s.crossings_in_24h + s.crossings_out_24h,
      icon: ArrowUpRight,
      hint: (
        <span className="inline-flex items-center gap-2">
          <span className="inline-flex items-center gap-0.5">
            <ArrowDownLeft className="size-3" /> {s.crossings_in_24h} in
          </span>
          <span className="inline-flex items-center gap-0.5">
            <ArrowUpRight className="size-3" /> {s.crossings_out_24h} out
          </span>
        </span>
      ),
    },
  ];
  return (
    <div className="grid grid-cols-2 gap-3 sm:gap-4 lg:grid-cols-3 xl:grid-cols-5">
      {items.map((it) => (
        <Card key={it.label} className="p-4 sm:p-5">
          <div className="flex items-center justify-between">
            <p className="text-sm text-muted-foreground">{it.label}</p>
            <it.icon className="size-4 text-muted-foreground" aria-hidden />
          </div>
          <p className="mt-2 text-2xl font-bold sm:text-3xl tabular-nums" data-testid={`kpi-${it.label.toLowerCase().replace(/\s+/g, "-")}`}>
            {it.value}
          </p>
          <p className="mt-1 text-xs text-muted-foreground">{it.hint}</p>
        </Card>
      ))}
    </div>
  );
}
