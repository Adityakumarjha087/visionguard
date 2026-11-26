"use client";
import { useCallback, useEffect, useReducer, useRef, useState } from "react";
import { Check, Eraser, RefreshCw, Save, Trash2, Undo2 } from "lucide-react";
import { Field } from "@/components/common/field";
import { EmptyState } from "@/components/common/states";
import { ConfirmDialog } from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/native-select";
import { drawShape, drawZones, fitCanvas } from "@/lib/canvas-draw";
import { containRect, pixelToNorm } from "@/lib/overlay";
import type { Point, Zone, ZoneInput, ZoneKind } from "@/lib/types";
import { canFinish, initialDraft, zoneDraftReducer } from "@/lib/zone-draft";

const PALETTE = ["#22c55e", "#ef4444", "#3b82f6", "#eab308", "#a855f7", "#06b6d4"];

export interface ZoneEditorProps {
  imageSrc: string | null;
  zones: Zone[];
  canEdit: boolean;
  onSave: (z: ZoneInput) => Promise<void>;
  onDelete: (z: Zone) => Promise<void>;
  onRefreshImage?: () => void;
}

export function ZoneEditor({ imageSrc, zones, canEdit, onSave, onDelete, onRefreshImage }: ZoneEditorProps) {
  const [draft, dispatch] = useReducer(zoneDraftReducer, undefined, () => initialDraft("polygon"));
  const [name, setName] = useState("");
  const [color, setColor] = useState(PALETTE[0]!);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const imgRef = useRef<HTMLImageElement>(null);

  const contentRect = useCallback(() => {
    const c = canvasRef.current;
    const img = imgRef.current;
    if (!c) return null;
    const r = c.getBoundingClientRect();
    return { box: r, rect: containRect(r.width, r.height, img?.naturalWidth ?? 0, img?.naturalHeight ?? 0) };
  }, []);

  const redraw = useCallback(() => {
    const c = canvasRef.current;
    if (!c) return;
    const { ctx } = fitCanvas(c);
    const cr = contentRect();
    if (!ctx || !cr) return;
    drawZones(ctx, zones, cr.rect);
    if (draft.points.length) {
      drawShape(ctx, draft.points, draft.kind, color, cr.rect, name || undefined, { closed: draft.closed, vertices: true });
    }
  }, [zones, draft, color, name, contentRect]);

  useEffect(() => {
    redraw();
    const c = canvasRef.current;
    if (!c || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(() => redraw());
    ro.observe(c);
    return () => ro.disconnect();
  }, [redraw]);

  function onClick(e: React.MouseEvent<HTMLCanvasElement>) {
    if (!canEdit || e.detail > 1) return; // second click of a double-click is ignored
    const cr = contentRect();
    if (!cr) return;
    const p = pixelToNorm(e.clientX - cr.box.left, e.clientY - cr.box.top, cr.rect);
    if (p) dispatch({ type: "add", point: p as Point });
  }

  async function save() {
    setError(null);
    if (!name.trim()) return setError("Give the zone a name");
    if (!canFinish(draft)) return setError(draft.kind === "line" ? "A line needs exactly 2 points" : "A polygon needs at least 3 points");
    setSaving(true);
    try {
      await onSave({ name: name.trim(), kind: draft.kind, points: draft.points, color });
      dispatch({ type: "clear" });
      setName("");
    } catch (e) {
      setError(e instanceof Error ? e.message : "Save failed");
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="grid gap-6 xl:grid-cols-[1fr_340px]">
      <Card className="overflow-hidden">
        <div className="relative aspect-video bg-black">
          {imageSrc ? (
            <img ref={imgRef} src={imageSrc} alt="Still frame for zone drawing" className="size-full object-contain" onLoad={redraw} />
          ) : null}
          <canvas
            ref={canvasRef}
            data-testid="zone-canvas"
            aria-label={canEdit ? "Zone drawing canvas: click to add points, double-click to finish" : "Zones"}
            className={`absolute inset-0 size-full ${canEdit ? "cursor-crosshair" : ""}`}
            onClick={onClick}
            onDoubleClick={() => dispatch({ type: "finish" })}
          />
        </div>
        <div className="flex flex-wrap items-center gap-2 border-t p-3 text-sm">
          <Badge variant="outline" data-testid="draft-points">
            {draft.points.length} {draft.points.length === 1 ? "point" : "points"}
          </Badge>
          {draft.closed ? <Badge variant="success">closed</Badge> : null}
          <span className="text-xs text-muted-foreground">
            {draft.kind === "line"
              ? "Click two points. “In” = moving to the left of p0→p1."
              : "Click to add vertices; double-click or Finish to close."}
          </span>
          {onRefreshImage ? (
            <Button variant="ghost" size="sm" className="ml-auto" onClick={onRefreshImage}>
              <RefreshCw /> New frame
            </Button>
          ) : null}
        </div>
      </Card>

      <div className="space-y-6">
        {canEdit ? (
          <Card>
            <CardHeader>
              <CardTitle>New zone</CardTitle>
            </CardHeader>
            <CardContent className="grid gap-4">
              <Field id="zone-name" label="Name">
                <Input id="zone-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="Restricted area" />
              </Field>
              <div className="grid grid-cols-2 gap-3">
                <Field id="zone-kind" label="Kind">
                  <NativeSelect id="zone-kind" value={draft.kind} onChange={(e) => dispatch({ type: "kind", kind: e.target.value as ZoneKind })}>
                    <option value="polygon">Polygon</option>
                    <option value="line">Line</option>
                  </NativeSelect>
                </Field>
                <Field id="zone-color" label="Colour">
                  <Input id="zone-color" type="color" value={color} onChange={(e) => setColor(e.target.value)} className="h-9 p-1" />
                </Field>
              </div>
              <div className="flex flex-wrap gap-1.5" aria-label="Colour presets">
                {PALETTE.map((c) => (
                  <button
                    key={c}
                    type="button"
                    aria-label={`Colour ${c}`}
                    className="size-6 cursor-pointer rounded-full border-2 border-background ring-1 ring-border"
                    style={{ background: c, outline: c === color ? `2px solid ${c}` : undefined }}
                    onClick={() => setColor(c)}
                  />
                ))}
              </div>
              <div className="flex flex-wrap gap-2">
                <Button variant="outline" size="sm" onClick={() => dispatch({ type: "undo" })} disabled={!draft.points.length}>
                  <Undo2 /> Undo
                </Button>
                <Button variant="outline" size="sm" onClick={() => dispatch({ type: "clear" })} disabled={!draft.points.length}>
                  <Eraser /> Clear
                </Button>
                {draft.kind === "polygon" ? (
                  <Button variant="outline" size="sm" onClick={() => dispatch({ type: "finish" })} disabled={!canFinish(draft) || draft.closed}>
                    <Check /> Finish
                  </Button>
                ) : null}
              </div>
              {error ? (
                <p role="alert" className="text-sm text-destructive">
                  {error}
                </p>
              ) : null}
              <Button onClick={save} disabled={saving}>
                <Save /> Save zone
              </Button>
            </CardContent>
          </Card>
        ) : null}

        <Card>
          <CardHeader>
            <CardTitle>Zones ({zones.length})</CardTitle>
          </CardHeader>
          <CardContent>
            {!zones.length ? (
              <EmptyState title="No zones" description="Draw a polygon or line on the frame." />
            ) : (
              <ul className="divide-y">
                {zones.map((z) => (
                  <li key={z.id} className="flex items-center gap-3 py-2" data-testid="zone-item">
                    <span className="size-3 rounded-full" style={{ background: z.color }} aria-hidden />
                    <span className="min-w-0 flex-1 truncate text-sm font-medium">{z.name}</span>
                    <Badge variant="outline">{z.kind}</Badge>
                    {canEdit ? (
                      <ConfirmDialog
                        title={`Delete zone "${z.name}"?`}
                        description="Rules that use this zone will stop working."
                        onConfirm={() => onDelete(z)}
                        trigger={
                          <Button variant="ghost" size="icon" aria-label={`Delete zone ${z.name}`}>
                            <Trash2 className="text-destructive" />
                          </Button>
                        }
                      />
                    ) : null}
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
