import { ShieldCheck } from "lucide-react";
import { IncidentStatusBadge, SeverityBadge } from "@/components/common/badges";
import { EmptyState } from "@/components/common/states";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { RULE_LABELS } from "@/lib/rules";
import type { Incident } from "@/lib/types";
import { formatDateTime } from "@/lib/utils";

export function IncidentsTable({ items, onOpen }: { items: Incident[]; onOpen: (i: Incident) => void }) {
  if (!items.length) return <EmptyState icon={ShieldCheck} title="No incidents" description="Nothing matches these filters." className="m-4" />;
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Time</TableHead>
          <TableHead>Camera</TableHead>
          <TableHead>Rule</TableHead>
          <TableHead>Message</TableHead>
          <TableHead>Severity</TableHead>
          <TableHead>Status</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {items.map((i) => (
          <TableRow
            key={i.id}
            data-testid="incident-row"
            tabIndex={0}
            className="cursor-pointer"
            onClick={() => onOpen(i)}
            onKeyDown={(e) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                onOpen(i);
              }
            }}
            aria-label={`Open incident ${i.rule_name} at ${formatDateTime(i.occurred_at)}`}
          >
            <TableCell className="whitespace-nowrap text-xs tabular-nums">
              <time dateTime={i.occurred_at}>{formatDateTime(i.occurred_at)}</time>
            </TableCell>
            <TableCell className="whitespace-nowrap">{i.camera_name}</TableCell>
            <TableCell>
              <div className="font-medium">{i.rule_name}</div>
              <div className="text-xs text-muted-foreground">{RULE_LABELS[i.rule_type] ?? i.rule_type}</div>
            </TableCell>
            <TableCell className="max-w-80">
              <p className="line-clamp-2 text-sm">{i.message}</p>
            </TableCell>
            <TableCell>
              <SeverityBadge severity={i.severity} />
            </TableCell>
            <TableCell>
              <IncidentStatusBadge status={i.status} />
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
