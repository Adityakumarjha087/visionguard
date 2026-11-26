"use client";
import Link from "next/link";
import { CameraOff, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { useAuth } from "@/components/auth/auth-provider";
import { CameraStateBadge } from "@/components/common/badges";
import { EmptyState, ErrorState, PageHeader, TableSkeleton } from "@/components/common/states";
import { AddCameraDialog } from "@/components/cameras/add-camera-dialog";
import { ConfirmDialog } from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useCameras } from "@/hooks/use-cameras";
import { api, errorMessage } from "@/lib/api";
import type { Camera, CameraPatch } from "@/lib/types";

export default function CamerasPage() {
  const { can } = useAuth();
  const canEdit = can("operator");
  const { data, error, loading, reload, setData } = useCameras();

  async function patch(c: Camera, body: CameraPatch) {
    setData((prev) => prev?.map((x) => (x.id === c.id ? { ...x, ...body } : x)));
    try {
      const updated = await api.updateCamera(c.id, body);
      setData((prev) => prev?.map((x) => (x.id === c.id ? updated : x)));
    } catch (e) {
      setData((prev) => prev?.map((x) => (x.id === c.id ? c : x)));
      toast.error("Update failed", { description: errorMessage(e) });
    }
  }

  async function remove(c: Camera) {
    try {
      await api.deleteCamera(c.id);
      setData((prev) => prev?.filter((x) => x.id !== c.id));
      toast.success(`Deleted "${c.name}"`);
    } catch (e) {
      toast.error("Delete failed", { description: errorMessage(e) });
    }
  }

  return (
    <>
      <PageHeader
        title="Cameras"
        description="Video sources processed by the inference service"
        actions={canEdit ? <AddCameraDialog onCreated={(c) => setData((prev) => [...(prev ?? []), c])} /> : null}
      />
      <Card>
        {error ? (
          <ErrorState error={error} onRetry={reload} className="m-4" />
        ) : loading && !data ? (
          <div className="p-4">
            <TableSkeleton />
          </div>
        ) : !data?.length ? (
          <EmptyState icon={CameraOff} title="No cameras yet" description={canEdit ? "Use “Add camera” to connect a source." : "Ask an operator to add a camera."} className="m-4" />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Source</TableHead>
                <TableHead>Status</TableHead>
                <TableHead className="text-right">Latency</TableHead>
                <TableHead>Enabled</TableHead>
                <TableHead>Privacy</TableHead>
                {canEdit ? <TableHead className="sr-only">Actions</TableHead> : null}
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.map((c) => (
                <TableRow key={c.id} data-testid="camera-row">
                  <TableCell className="font-medium">
                    <Link href={`/cameras/${c.id}`} className="hover:underline">
                      {c.name}
                    </Link>
                  </TableCell>
                  <TableCell className="max-w-64">
                    <span className="mr-2 text-xs font-semibold uppercase text-muted-foreground">{c.source_type}</span>
                    <span className="truncate font-mono text-xs">{c.source_uri}</span>
                  </TableCell>
                  <TableCell>
                    <CameraStateBadge state={c.status?.state ?? "offline"} fps={c.status?.fps} />
                  </TableCell>
                  <TableCell className="text-right font-mono text-xs tabular-nums">
                    {c.status?.latency_ms ? `${c.status.latency_ms.toFixed(0)} ms` : "—"}
                  </TableCell>
                  <TableCell>
                    <Switch
                      checked={c.enabled}
                      disabled={!canEdit}
                      onCheckedChange={(v) => void patch(c, { enabled: v })}
                      aria-label={`Enable ${c.name}`}
                    />
                  </TableCell>
                  <TableCell>
                    <Switch
                      checked={c.privacy_mode}
                      disabled={!canEdit}
                      onCheckedChange={(v) => void patch(c, { privacy_mode: v })}
                      aria-label={`Privacy mode for ${c.name}`}
                    />
                  </TableCell>
                  {canEdit ? (
                    <TableCell className="text-right">
                      <ConfirmDialog
                        title={`Delete "${c.name}"?`}
                        description="Zones, rules and the live pipeline for this camera will be removed. Incidents are kept."
                        onConfirm={() => remove(c)}
                        trigger={
                          <Button variant="ghost" size="icon" aria-label={`Delete ${c.name}`}>
                            <Trash2 className="text-destructive" />
                          </Button>
                        }
                      />
                    </TableCell>
                  ) : null}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Card>
    </>
  );
}
