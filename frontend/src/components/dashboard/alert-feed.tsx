import Link from "next/link";
import { BellRing, ImageOff } from "lucide-react";
import { SeverityBadge } from "@/components/common/badges";
import { EmptyState } from "@/components/common/states";
import { mediaUrl } from "@/lib/config";
import type { Incident } from "@/lib/types";
import { formatRelative } from "@/lib/utils";

export function AlertFeed({ items, token }: { items: Incident[]; token: string | null }) {
  if (!items.length) return <EmptyState icon={BellRing} title="No alerts yet" description="New alerts will appear here in real time." />;
  return (
    <ul className="divide-y" aria-live="polite" aria-label="Live alerts">
      {items.map((i) => (
        <li key={i.id}>
          <Link href={`/incidents?id=${i.id}`} className="flex gap-3 rounded-md p-2 hover:bg-accent" data-testid="alert-item">
            <div className="relative aspect-video w-24 shrink-0 overflow-hidden rounded-md bg-muted">
              {i.snapshot_url && token ? (
                <img src={mediaUrl(i.snapshot_url, token)} alt={`Snapshot: ${i.message}`} className="size-full object-cover" loading="lazy" />
              ) : (
                <ImageOff className="absolute inset-0 m-auto size-5 text-muted-foreground" aria-hidden />
              )}
            </div>
            <div className="min-w-0 flex-1 space-y-1">
              <div className="flex items-center justify-between gap-2">
                <p className="truncate text-sm font-medium">{i.rule_name}</p>
                <SeverityBadge severity={i.severity} />
              </div>
              <p className="line-clamp-2 text-xs text-muted-foreground">{i.message}</p>
              <p className="text-xs text-muted-foreground">
                {i.camera_name} · <time dateTime={i.occurred_at}>{formatRelative(i.occurred_at)}</time>
              </p>
            </div>
          </Link>
        </li>
      ))}
    </ul>
  );
}
