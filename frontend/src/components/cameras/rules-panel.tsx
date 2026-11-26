"use client";
import { useState } from "react";
import { Bell, Mail, Pencil, Plus, Send, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { SeverityBadge } from "@/components/common/badges";
import { EmptyState, ErrorState, TableSkeleton } from "@/components/common/states";
import { ConfirmDialog } from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useAsync } from "@/hooks/use-async";
import { api, errorMessage } from "@/lib/api";
import { RULE_LABELS } from "@/lib/rules";
import type { Rule, RuleInput, Zone } from "@/lib/types";
import { RuleForm, draftFromRule, toRuleInput } from "./rule-form";

function paramSummary(r: Rule): string {
  const p = r.params ?? {};
  const parts: string[] = [];
  if (p.classes?.length) parts.push(p.classes.join(", "));
  if (r.type === "dwell_time" && p.seconds !== undefined) parts.push(`> ${p.seconds}s`);
  if (r.type === "missing_ppe" && p.seconds !== undefined) parts.push(`grace ${p.seconds}s`);
  if (p.max_count !== undefined) parts.push(`> ${p.max_count} people`);
  return parts.join(" · ") || "—";
}

export function RulesPanel({ cameraId, zones, canEdit }: { cameraId: string; zones: Zone[]; canEdit: boolean }) {
  const rules = useAsync(() => api.listRules(cameraId), [cameraId]);
  const [editing, setEditing] = useState<Rule | "new" | null>(null);
  const zoneName = (id: string | null) => (id ? (zones.find((z) => z.id === id)?.name ?? "deleted zone") : "Whole frame");

  async function save(input: RuleInput) {
    try {
      if (editing && editing !== "new") {
        const r = await api.updateRule(editing.id, input);
        rules.setData((prev) => prev?.map((x) => (x.id === r.id ? r : x)));
        toast.success("Rule updated");
      } else {
        const r = await api.createRule(cameraId, input);
        rules.setData((prev) => [...(prev ?? []), r]);
        toast.success("Rule created");
      }
      setEditing(null);
    } catch (e) {
      toast.error("Could not save rule", { description: errorMessage(e) });
    }
  }

  async function toggle(r: Rule, enabled: boolean) {
    try {
      const updated = await api.updateRule(r.id, toRuleInput({ ...draftFromRule(r), enabled }));
      rules.setData((prev) => prev?.map((x) => (x.id === r.id ? updated : x)));
    } catch (e) {
      toast.error("Update failed", { description: errorMessage(e) });
    }
  }

  async function remove(r: Rule) {
    try {
      await api.deleteRule(r.id);
      rules.setData((prev) => prev?.filter((x) => x.id !== r.id));
      toast.success("Rule deleted");
    } catch (e) {
      toast.error("Delete failed", { description: errorMessage(e) });
    }
  }

  return (
    <div className="space-y-4">
      {canEdit ? (
        <div className="flex justify-end">
          <Button onClick={() => setEditing("new")}>
            <Plus /> New rule
          </Button>
        </div>
      ) : null}
      <Card>
        {rules.error ? (
          <ErrorState error={rules.error} onRetry={rules.reload} className="m-4" />
        ) : rules.loading && !rules.data ? (
          <div className="p-4">
            <TableSkeleton rows={3} />
          </div>
        ) : !rules.data?.length ? (
          <EmptyState icon={Bell} title="No rules" description="Rules turn detections into incidents." className="m-4" />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>Zone</TableHead>
                <TableHead>Params</TableHead>
                <TableHead>Severity</TableHead>
                <TableHead>Notify</TableHead>
                <TableHead>Enabled</TableHead>
                {canEdit ? <TableHead className="sr-only">Actions</TableHead> : null}
              </TableRow>
            </TableHeader>
            <TableBody>
              {rules.data.map((r) => (
                <TableRow key={r.id} data-testid="rule-row">
                  <TableCell className="font-medium">{r.name}</TableCell>
                  <TableCell>
                    <Badge variant="outline">{RULE_LABELS[r.type] ?? r.type}</Badge>
                  </TableCell>
                  <TableCell className="text-sm">{zoneName(r.zone_id)}</TableCell>
                  <TableCell className="text-xs text-muted-foreground">{paramSummary(r)}</TableCell>
                  <TableCell>
                    <SeverityBadge severity={r.severity} />
                  </TableCell>
                  <TableCell>
                    <span className="flex gap-1 text-muted-foreground">
                      {r.notify_email ? <Mail className="size-4" aria-label="Email" /> : null}
                      {r.notify_telegram ? <Send className="size-4" aria-label="Telegram" /> : null}
                      {!r.notify_email && !r.notify_telegram ? "—" : null}
                    </span>
                  </TableCell>
                  <TableCell>
                    <Switch checked={r.enabled} disabled={!canEdit} onCheckedChange={(v) => void toggle(r, v)} aria-label={`Enable rule ${r.name}`} />
                  </TableCell>
                  {canEdit ? (
                    <TableCell className="whitespace-nowrap text-right">
                      <Button variant="ghost" size="icon" aria-label={`Edit rule ${r.name}`} onClick={() => setEditing(r)}>
                        <Pencil />
                      </Button>
                      <ConfirmDialog
                        title={`Delete rule "${r.name}"?`}
                        onConfirm={() => remove(r)}
                        trigger={
                          <Button variant="ghost" size="icon" aria-label={`Delete rule ${r.name}`}>
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

      <Dialog open={editing !== null} onOpenChange={(o) => !o && setEditing(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{editing && editing !== "new" ? "Edit rule" : "New rule"}</DialogTitle>
            <DialogDescription>Parameters change with the rule type.</DialogDescription>
          </DialogHeader>
          {editing !== null ? (
            <RuleForm
              key={editing === "new" ? "new" : editing.id}
              zones={zones}
              initial={editing === "new" ? undefined : editing}
              onSubmit={save}
              onCancel={() => setEditing(null)}
            />
          ) : null}
        </DialogContent>
      </Dialog>
    </div>
  );
}
