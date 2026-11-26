"use client";
import { useState } from "react";
import { CheckCheck, Eye, Film, ImageOff, Loader2, Sparkles } from "lucide-react";
import { IncidentStatusBadge, SeverityBadge } from "@/components/common/badges";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { mediaUrl } from "@/lib/config";
import { RULE_LABELS } from "@/lib/rules";
import type { Incident, IncidentStatus } from "@/lib/types";
import { formatDateTime } from "@/lib/utils";

export function IncidentDetail({
  incident: i,
  token,
  canAct,
  onStatus,
}: {
  incident: Incident;
  token: string | null;
  canAct: boolean;
  onStatus: (s: IncidentStatus) => Promise<void>;
}) {
  const [busy, setBusy] = useState<IncidentStatus | null>(null);
  const act = async (s: IncidentStatus) => {
    setBusy(s);
    try {
      await onStatus(s);
    } finally {
      setBusy(null);
    }
  };

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center gap-2">
        <SeverityBadge severity={i.severity} />
        <IncidentStatusBadge status={i.status} />
        <Badge variant="outline">{RULE_LABELS[i.rule_type] ?? i.rule_type}</Badge>
      </div>
      <p className="text-sm">{i.message}</p>

      <section aria-label="Snapshot" className="overflow-hidden rounded-lg border bg-black">
        {i.snapshot_url && token ? (
          <img src={mediaUrl(i.snapshot_url, token)} alt={`Snapshot of incident: ${i.message}`} className="w-full object-contain" />
        ) : (
          <div className="grid aspect-video place-items-center text-sm text-white/70">
            <span className="flex items-center gap-2">
              <ImageOff className="size-4" /> No snapshot
            </span>
          </div>
        )}
      </section>

      <section aria-label="Video clip" className="space-y-2">
        <h3 className="flex items-center gap-2 text-sm font-semibold">
          <Film className="size-4" /> Clip
        </h3>
        {i.clip_url && token ? (
          <video controls preload="metadata" className="w-full rounded-lg border bg-black" src={mediaUrl(i.clip_url, token)} data-testid="incident-clip">
            <track kind="captions" />
          </video>
        ) : (
          <p className="flex items-center gap-2 rounded-lg border border-dashed p-4 text-sm text-muted-foreground" data-testid="clip-processing">
            <Loader2 className="size-4 animate-spin" /> Clip processing…
          </p>
        )}
      </section>

      <section aria-label="AI description" className="space-y-2 rounded-lg border bg-muted/40 p-4">
        <h3 className="flex items-center gap-2 text-sm font-semibold">
          <Sparkles className="size-4 text-primary" /> AI description
          {i.description_provider ? (
            <Badge variant="secondary" data-testid="description-provider">
              {i.description_provider}
            </Badge>
          ) : null}
        </h3>
        {i.description ? (
          <p className="text-sm leading-relaxed" data-testid="incident-description">
            {i.description}
          </p>
        ) : (
          <p className="flex items-center gap-2 text-sm text-muted-foreground" data-testid="description-pending">
            <Loader2 className="size-4 animate-spin" /> Generating description…
          </p>
        )}
      </section>

      <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
        <dt className="text-muted-foreground">Occurred</dt>
        <dd>{formatDateTime(i.occurred_at)}</dd>
        <dt className="text-muted-foreground">Camera</dt>
        <dd>{i.camera_name}</dd>
        <dt className="text-muted-foreground">Rule</dt>
        <dd>{i.rule_name}</dd>
        <dt className="text-muted-foreground">Object</dt>
        <dd>{i.object_class || "—"}</dd>
        <dt className="text-muted-foreground">Track IDs</dt>
        <dd className="font-mono">{i.track_ids?.length ? i.track_ids.join(", ") : "—"}</dd>
        <dt className="text-muted-foreground">Acknowledged by</dt>
        <dd>{i.acknowledged_by ?? "—"}</dd>
        <dt className="text-muted-foreground">Updated</dt>
        <dd>{formatDateTime(i.updated_at)}</dd>
        <dt className="text-muted-foreground">ID</dt>
        <dd className="break-all font-mono text-xs">{i.id}</dd>
      </dl>

      {canAct && i.status !== "resolved" ? (
        <div className="flex gap-2">
          {i.status === "open" ? (
            <Button variant="outline" onClick={() => act("acknowledged")} disabled={!!busy}>
              {busy === "acknowledged" ? <Loader2 className="animate-spin" /> : <Eye />} Acknowledge
            </Button>
          ) : null}
          <Button onClick={() => act("resolved")} disabled={!!busy}>
            {busy === "resolved" ? <Loader2 className="animate-spin" /> : <CheckCheck />} Resolve
          </Button>
        </div>
      ) : null}
    </div>
  );
}
