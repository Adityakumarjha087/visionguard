import Link from "next/link";
import { EyeOff } from "lucide-react";
import { CameraStateBadge } from "@/components/common/badges";
import { LiveStream } from "@/components/cameras/live-stream";
import { Card } from "@/components/ui/card";
import type { Camera } from "@/lib/types";

export function CameraTile({ camera }: { camera: Camera }) {
  return (
    <Card className="overflow-hidden">
      <Link href={`/cameras/${camera.id}`} className="block focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <LiveStream cameraId={camera.id} name={camera.name} disabled={!camera.enabled} className="aspect-video" />
        <div className="flex items-center justify-between gap-2 p-3">
          <div className="min-w-0">
            <p className="truncate font-medium">{camera.name}</p>
            <p className="truncate text-xs uppercase text-muted-foreground">{camera.source_type}</p>
          </div>
          <div className="flex items-center gap-1.5">
            {camera.privacy_mode ? <EyeOff className="size-4 text-muted-foreground" aria-label="Privacy mode on" /> : null}
            <CameraStateBadge state={camera.status?.state ?? "offline"} fps={camera.status?.fps} />
          </div>
        </div>
      </Link>
    </Card>
  );
}
