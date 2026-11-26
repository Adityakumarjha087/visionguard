"use client";
import { useEffect, useMemo, useState } from "react";
import { BarChart3, Flame, LineChart as LineIcon } from "lucide-react";
import { HeatmapCanvas, HeatmapLegend } from "@/components/analytics/heatmap-canvas";
import { CountsChart, CrossingsChart } from "@/components/analytics/charts";
import { EmptyState, ErrorState, PageHeader } from "@/components/common/states";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import { NativeSelect } from "@/components/ui/native-select";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useAsync } from "@/hooks/use-async";
import { useAccessToken } from "@/hooks/use-media-token";
import { api } from "@/lib/api";
import { RANGES, crossingTotals, pivotCounts, pivotCrossings, rangeParams, type RangeKey } from "@/lib/analytics";
import { mediaUrl } from "@/lib/config";

export default function AnalyticsPage() {
  const cameras = useAsync(() => api.listCameras(), []);
  const [cameraId, setCameraId] = useState<string>("");
  const [range, setRange] = useState<RangeKey>("1h");
  const token = useAccessToken();

  useEffect(() => {
    if (!cameraId && cameras.data?.[0]) setCameraId(cameras.data[0].id);
  }, [cameras.data, cameraId]);

  const enabled = !!cameraId;
  const counts = useAsync(() => {
    const p = rangeParams(range);
    return api.counts({ camera_id: cameraId, from: p.from, to: p.to, bucket: p.bucket });
  }, [cameraId, range], enabled);
  const heat = useAsync(() => {
    const p = rangeParams(range);
    return api.heatmap({ camera_id: cameraId, from: p.from, to: p.to, grid: 32 });
  }, [cameraId, range], enabled);
  const cross = useAsync(() => {
    const p = rangeParams(range);
    return api.lineCrossings({ camera_id: cameraId, from: p.from, to: p.to, bucket: p.crossBucket });
  }, [cameraId, range], enabled);

  const countRows = useMemo(() => pivotCounts(counts.data ?? []), [counts.data]);
  const crossRows = useMemo(() => pivotCrossings(cross.data ?? []), [cross.data]);
  const totals = useMemo(() => crossingTotals(cross.data ?? []), [cross.data]);
  const snapshot = cameraId && token ? mediaUrl(`/api/v1/cameras/${cameraId}/snapshot`, token) : null;

  if (cameras.error) return <ErrorState error={cameras.error} onRetry={cameras.reload} />;

  return (
    <>
      <PageHeader
        title="Analytics"
        description="Object counts, movement heatmap and line crossings"
        actions={
          <>
            <Label htmlFor="a-camera" className="sr-only">
              Camera
            </Label>
            <NativeSelect id="a-camera" className="w-56" value={cameraId} onChange={(e) => setCameraId(e.target.value)} disabled={!cameras.data?.length}>
              {!cameras.data?.length ? <option value="">No cameras</option> : null}
              {cameras.data?.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </NativeSelect>
            <Tabs value={range} onValueChange={(v) => setRange(v as RangeKey)}>
              <TabsList aria-label="Time range">
                {(Object.keys(RANGES) as RangeKey[]).map((k) => (
                  <TabsTrigger key={k} value={k} aria-label={RANGES[k].label}>
                    {k}
                  </TabsTrigger>
                ))}
              </TabsList>
            </Tabs>
          </>
        }
      />

      {cameras.data && !cameras.data.length ? (
        <EmptyState title="No cameras" description="Analytics appear once a camera is processing video." />
      ) : (
        <div className="grid gap-6 xl:grid-cols-2">
          <Card className="xl:col-span-2">
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <LineIcon className="size-4" /> People &amp; vehicles over time
              </CardTitle>
              <CardDescription>Average objects in frame per {RANGES[range].bucket} bucket</CardDescription>
            </CardHeader>
            <CardContent>
              {counts.error ? (
                <ErrorState error={counts.error} onRetry={counts.reload} />
              ) : counts.loading && !counts.data ? (
                <Skeleton className="h-[280px]" />
              ) : countRows.length ? (
                <CountsChart data={countRows} />
              ) : (
                <EmptyState title="No counts in this range" />
              )}
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <Flame className="size-4" /> Movement heatmap
              </CardTitle>
              <CardDescription>Where detections were observed</CardDescription>
            </CardHeader>
            <CardContent className="space-y-3">
              {heat.error ? (
                <ErrorState error={heat.error} onRetry={heat.reload} />
              ) : heat.data ? (
                <>
                  <HeatmapCanvas heatmap={heat.data} imageSrc={snapshot} />
                  <HeatmapLegend max={heat.data.max} />
                </>
              ) : (
                <Skeleton className="aspect-video" />
              )}
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <BarChart3 className="size-4" /> Line crossings
              </CardTitle>
              <CardDescription>
                <span data-testid="crossings-in">{totals.in}</span> in · <span data-testid="crossings-out">{totals.out}</span> out
              </CardDescription>
            </CardHeader>
            <CardContent>
              {cross.error ? (
                <ErrorState error={cross.error} onRetry={cross.reload} />
              ) : cross.loading && !cross.data ? (
                <Skeleton className="h-[260px]" />
              ) : crossRows.length ? (
                <CrossingsChart data={crossRows} />
              ) : (
                <EmptyState title="No crossings" description="Draw a line zone and add a line_crossing rule." />
              )}
            </CardContent>
          </Card>
        </div>
      )}
    </>
  );
}
