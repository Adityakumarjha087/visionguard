"use client";
import { useEffect, useState } from "react";
import Link from "next/link";
import { CameraOff } from "lucide-react";
import { CardGridSkeleton, EmptyState, ErrorState, PageHeader } from "@/components/common/states";
import { AlertFeed } from "@/components/dashboard/alert-feed";
import { CameraTile } from "@/components/dashboard/camera-tile";
import { KpiCards } from "@/components/dashboard/kpi-cards";
import { useWsEvent } from "@/components/realtime-provider";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { useAsync } from "@/hooks/use-async";
import { useCameras } from "@/hooks/use-cameras";
import { useAccessToken } from "@/hooks/use-media-token";
import { api } from "@/lib/api";
import type { Incident } from "@/lib/types";

const FEED_SIZE = 15;

export default function DashboardPage() {
  const summary = useAsync(() => api.summary(), []);
  const cameras = useCameras();
  const initial = useAsync(() => api.listIncidents({ page: 1, page_size: FEED_SIZE }), []);
  const [feed, setFeed] = useState<Incident[]>([]);
  const token = useAccessToken();

  useEffect(() => {
    if (initial.data) setFeed(initial.data.items);
  }, [initial.data]);

  // refresh KPIs periodically
  const reloadSummary = summary.reload;
  useEffect(() => {
    const t = setInterval(() => void reloadSummary(), 30_000);
    return () => clearInterval(t);
  }, [reloadSummary]);

  useWsEvent("alert", ({ data }) => {
    setFeed((prev) => [data, ...prev.filter((p) => p.id !== data.id)].slice(0, FEED_SIZE));
    summary.setData((s) => (s ? { ...s, open_incidents: s.open_incidents + 1, incidents_24h: s.incidents_24h + 1 } : s));
  });
  useWsEvent("incident_update", ({ data }) => setFeed((prev) => prev.map((p) => (p.id === data.id ? data : p))));

  return (
    <>
      <PageHeader title="Dashboard" description="Live overview of cameras, detections and alerts" />
      {summary.error ? (
        <ErrorState error={summary.error} onRetry={summary.reload} />
      ) : summary.data ? (
        <KpiCards s={summary.data} />
      ) : (
        <CardGridSkeleton count={5} className="xl:grid-cols-5" />
      )}

      <div className="grid gap-6 xl:grid-cols-[1fr_380px]">
        <section aria-labelledby="cams-heading" className="space-y-3">
          <div className="flex items-center justify-between">
            <h2 id="cams-heading" className="text-lg font-semibold">
              Cameras
            </h2>
            <Button asChild variant="outline" size="sm">
              <Link href="/cameras">Manage</Link>
            </Button>
          </div>
          {cameras.error ? (
            <ErrorState error={cameras.error} onRetry={cameras.reload} />
          ) : cameras.loading && !cameras.data ? (
            <div className="grid gap-4 md:grid-cols-2">
              {Array.from({ length: 4 }, (_, i) => (
                <Skeleton key={i} className="aspect-video rounded-xl" />
              ))}
            </div>
          ) : cameras.data?.length ? (
            <div className="grid gap-4 md:grid-cols-2">
              {cameras.data.map((c) => (
                <CameraTile key={c.id} camera={c} />
              ))}
            </div>
          ) : (
            <EmptyState icon={CameraOff} title="No cameras" description="Add a camera source to start monitoring." />
          )}
        </section>

        <Card className="h-fit">
          <CardHeader className="flex-row items-center justify-between pb-3">
            <CardTitle className="flex items-center gap-2">
              <span className="relative flex size-2">
                <span className="absolute inline-flex size-full animate-ping rounded-full bg-red-500 opacity-75" />
                <span className="relative inline-flex size-2 rounded-full bg-red-500" />
              </span>
              Live alerts
            </CardTitle>
            <Button asChild variant="ghost" size="sm">
              <Link href="/incidents">All incidents</Link>
            </Button>
          </CardHeader>
          <CardContent>
            {initial.error ? (
              <ErrorState error={initial.error} onRetry={initial.reload} />
            ) : initial.loading && !feed.length ? (
              <div className="space-y-2">
                {Array.from({ length: 4 }, (_, i) => (
                  <Skeleton key={i} className="h-16" />
                ))}
              </div>
            ) : (
              <AlertFeed items={feed} token={token} />
            )}
          </CardContent>
        </Card>
      </div>
    </>
  );
}
