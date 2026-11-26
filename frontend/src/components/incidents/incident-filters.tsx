"use client";
import { useEffect, useState } from "react";
import { Search, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { NativeSelect } from "@/components/ui/native-select";
import { RULE_LABELS, RULE_TYPES, SEVERITIES } from "@/lib/rules";
import type { Camera, IncidentFilters, IncidentStatus, RuleType, Severity } from "@/lib/types";

/** datetime-local value <-> ISO string helpers */
export function localToIso(v: string): string | undefined {
  if (!v) return undefined;
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? undefined : d.toISOString();
}
export function isoToLocal(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

const STATUSES: IncidentStatus[] = ["open", "acknowledged", "resolved"];

export function IncidentFiltersBar({
  value,
  cameras,
  onChange,
  debounceMs = 300,
}: {
  value: IncidentFilters;
  cameras: Camera[];
  onChange: (f: IncidentFilters) => void;
  debounceMs?: number;
}) {
  const [q, setQ] = useState(value.q ?? "");
  const set = (patch: Partial<IncidentFilters>) => onChange({ ...value, ...patch, page: 1 });

  useEffect(() => {
    if ((value.q ?? "") === q) return;
    const t = setTimeout(() => onChange({ ...value, q, page: 1 }), debounceMs);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q]);

  const active = !!(value.q || value.camera_id || value.severity || value.status || value.rule_type || value.from || value.to);

  return (
    <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4" role="search" aria-label="Incident filters">
      <div className="relative sm:col-span-2">
        <Label htmlFor="f-q" className="sr-only">
          Search incidents
        </Label>
        <Search className="pointer-events-none absolute left-2.5 top-2.5 size-4 text-muted-foreground" aria-hidden />
        <Input id="f-q" placeholder="Search message, description, rule…" value={q} onChange={(e) => setQ(e.target.value)} className="pl-8" />
      </div>
      <div>
        <Label htmlFor="f-camera" className="sr-only">
          Camera
        </Label>
        <NativeSelect id="f-camera" value={value.camera_id ?? ""} onChange={(e) => set({ camera_id: e.target.value || undefined })}>
          <option value="">All cameras</option>
          {cameras.map((c) => (
            <option key={c.id} value={c.id}>
              {c.name}
            </option>
          ))}
        </NativeSelect>
      </div>
      <div>
        <Label htmlFor="f-severity" className="sr-only">
          Severity
        </Label>
        <NativeSelect id="f-severity" value={value.severity ?? ""} onChange={(e) => set({ severity: e.target.value as Severity | "" })}>
          <option value="">All severities</option>
          {SEVERITIES.map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </NativeSelect>
      </div>
      <div>
        <Label htmlFor="f-status" className="sr-only">
          Status
        </Label>
        <NativeSelect id="f-status" value={value.status ?? ""} onChange={(e) => set({ status: e.target.value as IncidentStatus | "" })}>
          <option value="">All statuses</option>
          {STATUSES.map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </NativeSelect>
      </div>
      <div>
        <Label htmlFor="f-type" className="sr-only">
          Rule type
        </Label>
        <NativeSelect id="f-type" value={value.rule_type ?? ""} onChange={(e) => set({ rule_type: e.target.value as RuleType | "" })}>
          <option value="">All rule types</option>
          {RULE_TYPES.map((t) => (
            <option key={t} value={t}>
              {RULE_LABELS[t]}
            </option>
          ))}
        </NativeSelect>
      </div>
      <div>
        <Label htmlFor="f-from" className="sr-only">
          From
        </Label>
        <Input id="f-from" type="datetime-local" value={isoToLocal(value.from)} onChange={(e) => set({ from: localToIso(e.target.value) })} title="From" />
      </div>
      <div className="flex gap-2">
        <div className="flex-1">
          <Label htmlFor="f-to" className="sr-only">
            To
          </Label>
          <Input id="f-to" type="datetime-local" value={isoToLocal(value.to)} onChange={(e) => set({ to: localToIso(e.target.value) })} title="To" />
        </div>
        {active ? (
          <Button
            variant="ghost"
            size="icon"
            aria-label="Clear filters"
            onClick={() => {
              setQ("");
              onChange({ page: 1, page_size: value.page_size });
            }}
          >
            <X />
          </Button>
        ) : null}
      </div>
    </div>
  );
}
