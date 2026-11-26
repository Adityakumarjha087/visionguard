"use client";
import { Suspense, useCallback, useEffect, useRef, useState } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { toast } from "sonner";
import { useAuth } from "@/components/auth/auth-provider";
import { ErrorState, PageHeader, TableSkeleton } from "@/components/common/states";
import { IncidentDetail } from "@/components/incidents/incident-detail";
import { IncidentFiltersBar } from "@/components/incidents/incident-filters";
import { IncidentsTable } from "@/components/incidents/incidents-table";
import { useWsEvent } from "@/components/realtime-provider";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { useAsync } from "@/hooks/use-async";
import { useAccessToken } from "@/hooks/use-media-token";
import { api, errorMessage } from "@/lib/api";
import type { Incident, IncidentFilters, IncidentStatus } from "@/lib/types";

const PAGE_SIZE = 20;

function IncidentsInner() {
  const { can } = useAuth();
  const router = useRouter();
  const pathname = usePathname();
  const params = useSearchParams();
  const openId = params.get("id");
  const [filters, setFilters] = useState<IncidentFilters>({ page: 1, page_size: PAGE_SIZE });
  const list = useAsync((signal) => api.listIncidents(filters, signal), [JSON.stringify(filters)]);
  const cameras = useAsync(() => api.listCameras(), []);
  const [selected, setSelected] = useState<Incident | null>(null);
  const token = useAccessToken();
  const reloadTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  // open detail from ?id= (dashboard / toast links)
  useEffect(() => {
    if (!openId) {
      setSelected(null);
      return;
    }
    if (selected?.id === openId) return;
    const local = list.data?.items.find((x) => x.id === openId);
    if (local) setSelected(local);
    else
      api
        .getIncident(openId)
        .then(setSelected)
        .catch((e) => toast.error("Incident not found", { description: errorMessage(e) }));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [openId]);

  const open = (i: Incident) => {
    setSelected(i);
    router.replace(`${pathname}?id=${i.id}`, { scroll: false });
  };
  const close = () => {
    setSelected(null);
    router.replace(pathname, { scroll: false });
  };

  const { reload, setData } = list;
  const scheduleReload = useCallback(() => {
    if (reloadTimer.current) clearTimeout(reloadTimer.current);
    reloadTimer.current = setTimeout(() => void reload(), 400);
  }, [reload]);
  useEffect(() => () => {
    if (reloadTimer.current) clearTimeout(reloadTimer.current);
  }, []);

  useWsEvent("alert", () => {
    if ((filters.page ?? 1) === 1) scheduleReload();
  });
  useWsEvent("incident_update", ({ data }) => {
    setData((prev) => (prev ? { ...prev, items: prev.items.map((x) => (x.id === data.id ? data : x)) } : prev));
    setSelected((s) => (s?.id === data.id ? data : s));
  });

  async function setStatus(status: IncidentStatus) {
    if (!selected) return;
    try {
      const updated = await api.setIncidentStatus(selected.id, status);
      setSelected(updated);
      setData((prev) => (prev ? { ...prev, items: prev.items.map((x) => (x.id === updated.id ? updated : x)) } : prev));
      toast.success(`Incident ${status}`);
    } catch (e) {
      toast.error("Update failed", { description: errorMessage(e) });
    }
  }

  const page = list.data?.page ?? filters.page ?? 1;
  const total = list.data?.total ?? 0;
  const pages = Math.max(1, Math.ceil(total / (list.data?.page_size ?? PAGE_SIZE)));

  return (
    <>
      <PageHeader title="Incidents" description="Alerts raised by rules, with AI descriptions and clips" />
      <IncidentFiltersBar value={filters} cameras={cameras.data ?? []} onChange={setFilters} />
      <Card>
        {list.error ? (
          <ErrorState error={list.error} onRetry={list.reload} className="m-4" />
        ) : !list.data ? (
          <div className="p-4">
            <TableSkeleton />
          </div>
        ) : (
          <IncidentsTable items={list.data.items} onOpen={open} />
        )}
        <div className="flex items-center justify-between gap-2 border-t p-3 text-sm">
          <span className="text-muted-foreground" aria-live="polite">
            {total} incident{total === 1 ? "" : "s"} · page {page} of {pages}
          </span>
          <div className="flex gap-2">
            <Button
              variant="outline"
              size="sm"
              disabled={page <= 1 || list.loading}
              onClick={() => setFilters((f) => ({ ...f, page: page - 1 }))}
              aria-label="Previous page"
            >
              <ChevronLeft /> Prev
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={page >= pages || list.loading}
              onClick={() => setFilters((f) => ({ ...f, page: page + 1 }))}
              aria-label="Next page"
            >
              Next <ChevronRight />
            </Button>
          </div>
        </div>
      </Card>

      <Sheet open={!!selected} onOpenChange={(o) => !o && close()}>
        <SheetContent>
          {selected ? (
            <>
              <SheetHeader>
                <SheetTitle>{selected.rule_name}</SheetTitle>
                <SheetDescription>{selected.camera_name}</SheetDescription>
              </SheetHeader>
              <IncidentDetail incident={selected} token={token} canAct={can("operator")} onStatus={setStatus} />
            </>
          ) : null}
        </SheetContent>
      </Sheet>
    </>
  );
}

export default function IncidentsPage() {
  return (
    <Suspense fallback={<TableSkeleton />}>
      <IncidentsInner />
    </Suspense>
  );
}
