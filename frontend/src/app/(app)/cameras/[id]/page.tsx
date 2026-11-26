"use client";
import { useState } from "react";
import Link from "next/link";
import { useParams } from "next/navigation";
import { ArrowLeft, Bell, Pentagon, Video } from "lucide-react";
import { toast } from "sonner";
import { useAuth } from "@/components/auth/auth-provider";
import { CameraStateBadge } from "@/components/common/badges";
import { ErrorState } from "@/components/common/states";
import { LiveOverlay } from "@/components/cameras/live-overlay";
import { RulesPanel } from "@/components/cameras/rules-panel";
import { ZoneEditor } from "@/components/cameras/zone-editor";
import { useWsEvent } from "@/components/realtime-provider";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useAsync } from "@/hooks/use-async";
import { useAccessToken } from "@/hooks/use-media-token";
import { api, errorMessage } from "@/lib/api";
import { mediaUrl } from "@/lib/config";

export default function CameraDetailPage() {
  const { id } = useParams<{ id: string }>();
  const { can } = useAuth();
  const canEdit = can("operator");
  const camera = useAsync(() => api.getCamera(id), [id]);
  const zones = useAsync(() => api.listZones(id), [id]);
  const token = useAccessToken();
  const [snapKey, setSnapKey] = useState(() => Date.now());
  const [tab, setTab] = useState("live");

  useWsEvent("camera_status", ({ data }) => {
    if (data.camera_id !== id) return;
    const { camera_id: _c, ...status } = data;
    camera.setData((c) => (c ? { ...c, status: { ...c.status, ...status } } : c));
  });

  if (camera.error) return <ErrorState error={camera.error} onRetry={camera.reload} />;
  if (!camera.data) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="aspect-video w-full rounded-xl" />
      </div>
    );
  }
  const cam = camera.data;
  const zoneList = zones.data ?? [];
  const snapshot = token ? `${mediaUrl(`/api/v1/cameras/${id}/snapshot`, token)}&t=${snapKey}` : null;

  return (
    <>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex items-center gap-3">
          <Button asChild variant="ghost" size="icon" aria-label="Back to cameras">
            <Link href="/cameras">
              <ArrowLeft />
            </Link>
          </Button>
          <div>
            <h1 className="text-2xl font-bold tracking-tight">{cam.name}</h1>
            <p className="font-mono text-xs text-muted-foreground">
              {cam.source_type} · {cam.source_uri}
            </p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          {cam.privacy_mode ? <Badge variant="secondary">Privacy mode</Badge> : null}
          <CameraStateBadge state={cam.status?.state ?? "offline"} fps={cam.status?.fps} />
        </div>
      </div>
      {cam.status?.message && cam.status.state === "error" ? (
        <p role="alert" className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {cam.status.message}
        </p>
      ) : null}

      <Tabs value={tab} onValueChange={setTab}>
        <TabsList>
          <TabsTrigger value="live">
            <Video /> Live
          </TabsTrigger>
          <TabsTrigger value="zones">
            <Pentagon /> Zones
          </TabsTrigger>
          <TabsTrigger value="rules">
            <Bell /> Rules
          </TabsTrigger>
        </TabsList>
        <TabsContent value="live">
          <LiveOverlay camera={cam} zones={zoneList} />
        </TabsContent>
        <TabsContent value="zones">
          {zones.error ? (
            <ErrorState error={zones.error} onRetry={zones.reload} />
          ) : (
            <ZoneEditor
              imageSrc={tab === "zones" ? snapshot : null}
              zones={zoneList}
              canEdit={canEdit}
              onRefreshImage={() => setSnapKey(Date.now())}
              onSave={async (z) => {
                try {
                  const created = await api.createZone(id, z);
                  zones.setData((prev) => [...(prev ?? []), created]);
                  toast.success(`Zone "${created.name}" saved`);
                } catch (e) {
                  toast.error("Could not save zone", { description: errorMessage(e) });
                  throw e;
                }
              }}
              onDelete={async (z) => {
                try {
                  await api.deleteZone(z.id);
                  zones.setData((prev) => prev?.filter((x) => x.id !== z.id));
                  toast.success("Zone deleted");
                } catch (e) {
                  toast.error("Delete failed", { description: errorMessage(e) });
                }
              }}
            />
          )}
        </TabsContent>
        <TabsContent value="rules">
          <RulesPanel cameraId={id} zones={zoneList} canEdit={canEdit} />
        </TabsContent>
      </Tabs>
    </>
  );
}
