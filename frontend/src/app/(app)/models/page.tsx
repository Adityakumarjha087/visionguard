"use client";
import { useState } from "react";
import { Boxes, CheckCircle2, Loader2, Power, XCircle } from "lucide-react";
import { toast } from "sonner";
import { useAuth } from "@/components/auth/auth-provider";
import { LatencyChart } from "@/components/analytics/charts";
import { EmptyState, ErrorState, PageHeader, TableSkeleton } from "@/components/common/states";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useAsync } from "@/hooks/use-async";
import { api, errorMessage } from "@/lib/api";
import type { Model } from "@/lib/types";

const fmt = (v: number | null, digits = 3) => (v === null || v === undefined ? "—" : v.toFixed(digits));

export default function ModelsPage() {
  const { can } = useAuth();
  const isAdmin = can("admin");
  const models = useAsync(() => api.listModels(), []);
  const [busy, setBusy] = useState<string | null>(null);

  async function activate(m: Model) {
    setBusy(m.id);
    try {
      const updated = await api.activateModel(m.id);
      models.setData((prev) => prev?.map((x) => (x.id === updated.id ? updated : { ...x, active: false })));
      toast.success(`${updated.name} is now active`);
    } catch (e) {
      toast.error("Activation failed", { description: errorMessage(e) });
    } finally {
      setBusy(null);
    }
  }

  const latency = (models.data ?? [])
    .filter((m) => m.latency_ms !== null)
    .map((m) => ({ name: m.name, latency: Math.round((m.latency_ms ?? 0) * 10) / 10, active: m.active }));

  return (
    <>
      <PageHeader title="Models" description="Detection backends, accuracy and speed benchmarks" />
      <Card>
        {models.error ? (
          <ErrorState error={models.error} onRetry={models.reload} className="m-4" />
        ) : !models.data ? (
          <div className="p-4">
            <TableSkeleton rows={4} />
          </div>
        ) : !models.data.length ? (
          <EmptyState icon={Boxes} title="No models registered" className="m-4" />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Model</TableHead>
                <TableHead>Backend</TableHead>
                <TableHead>Classes</TableHead>
                <TableHead className="text-right">mAP50 %</TableHead>
                <TableHead className="text-right">mAP50-95 %</TableHead>
                <TableHead className="text-right">Latency</TableHead>
                <TableHead className="text-right">FPS</TableHead>
                <TableHead>Available</TableHead>
                <TableHead className="sr-only">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {models.data.map((m) => (
                <TableRow key={m.id} data-testid="model-row">
                  <TableCell>
                    <div className="flex items-center gap-2 font-medium">
                      {m.name}
                      {m.active ? <Badge variant="success">Active</Badge> : null}
                    </div>
                    {m.benchmark_source ? <p className="text-xs text-muted-foreground">{m.benchmark_source}</p> : null}
                  </TableCell>
                  <TableCell>
                    <Badge variant="outline" className="uppercase">
                      {m.backend}
                    </Badge>
                  </TableCell>
                  <TableCell className="max-w-56">
                    <p className="line-clamp-2 text-xs text-muted-foreground" title={m.classes.join(", ")}>
                      {m.classes.join(", ") || "—"}
                    </p>
                  </TableCell>
                  <TableCell className="text-right font-mono tabular-nums">{fmt(m.map50, 1)}</TableCell>
                  <TableCell className="text-right font-mono tabular-nums">{fmt(m.map50_95, 1)}</TableCell>
                  <TableCell className="text-right font-mono tabular-nums">{m.latency_ms === null ? "—" : `${fmt(m.latency_ms, 1)} ms`}</TableCell>
                  <TableCell className="text-right font-mono tabular-nums">{fmt(m.fps, 1)}</TableCell>
                  <TableCell>
                    {m.available ? (
                      <span className="inline-flex items-center gap-1 text-sm text-emerald-600 dark:text-emerald-400">
                        <CheckCircle2 className="size-4" /> Yes
                      </span>
                    ) : (
                      <span className="inline-flex items-center gap-1 text-sm text-muted-foreground">
                        <XCircle className="size-4" /> No weights
                      </span>
                    )}
                  </TableCell>
                  <TableCell className="text-right">
                    {isAdmin && !m.active ? (
                      <Button size="sm" variant="outline" disabled={!m.available || busy !== null} onClick={() => activate(m)} aria-label={`Activate ${m.name}`}>
                        {busy === m.id ? <Loader2 className="animate-spin" /> : <Power />} Activate
                      </Button>
                    ) : null}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Card>

      {latency.length ? (
        <Card>
          <CardHeader>
            <CardTitle>Inference latency</CardTitle>
            <CardDescription>Lower is better · per-frame latency on the inference host</CardDescription>
          </CardHeader>
          <CardContent>
            <LatencyChart data={latency} />
          </CardContent>
        </Card>
      ) : null}
    </>
  );
}
