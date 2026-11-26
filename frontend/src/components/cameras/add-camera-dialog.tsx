"use client";
import { useEffect, useState } from "react";
import { Loader2, Plus, Upload } from "lucide-react";
import { toast } from "sonner";
import { Field, SwitchRow } from "@/components/common/field";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { NativeSelect } from "@/components/ui/native-select";
import { Progress } from "@/components/ui/progress";
import { Switch } from "@/components/ui/switch";
import { api, errorMessage } from "@/lib/api";
import type { Camera, Sample, SourceType } from "@/lib/types";
import { formatBytes } from "@/lib/utils";

const MAX_UPLOAD = 200 * 1024 * 1024;

export function AddCameraDialog({ onCreated }: { onCreated: (c: Camera) => void }) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [type, setType] = useState<SourceType>("file");
  const [uri, setUri] = useState("");
  const [fileMode, setFileMode] = useState<"sample" | "upload">("sample");
  const [file, setFile] = useState<File | null>(null);
  const [samples, setSamples] = useState<Sample[] | null>(null);
  const [fps, setFps] = useState(10);
  const [privacy, setPrivacy] = useState(true);
  const [enabled, setEnabled] = useState(true);
  const [progress, setProgress] = useState<number | null>(null);
  const [pending, setPending] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});

  useEffect(() => {
    if (!open || samples) return;
    api
      .listSamples()
      .then((s) => {
        setSamples(s);
        if (s[0]) setUri((u) => u || s[0]!.source_uri);
      })
      .catch(() => setSamples([]));
  }, [open, samples]);

  function reset() {
    setName("");
    setType("file");
    setUri(samples?.[0]?.source_uri ?? "");
    setFileMode("sample");
    setFile(null);
    setFps(10);
    setPrivacy(true);
    setEnabled(true);
    setProgress(null);
    setErrors({});
  }

  function validate(): Record<string, string> {
    const e: Record<string, string> = {};
    if (!name.trim()) e.name = "Name is required";
    if (type === "rtsp" && !/^rtsps?:\/\/.+/i.test(uri.trim())) e.uri = "Enter an rtsp:// URL";
    if (type === "webcam" && !/^\d+$/.test(uri.trim())) e.uri = "Webcam index, e.g. 0";
    if (type === "file" && fileMode === "sample" && !uri) e.uri = "Pick a sample video";
    if (type === "file" && fileMode === "upload") {
      if (!file) e.file = "Choose a video file";
      else if (file.size > MAX_UPLOAD) e.file = "File exceeds 200 MB";
    }
    if (!(fps >= 1 && fps <= 60)) e.fps = "1-60";
    return e;
  }

  async function submit(ev: React.FormEvent) {
    ev.preventDefault();
    const e = validate();
    setErrors(e);
    if (Object.keys(e).length) return;
    setPending(true);
    try {
      let cam: Camera;
      if (type === "file" && fileMode === "upload" && file) {
        setProgress(0);
        cam = await api.uploadCamera(file, name.trim(), setProgress);
        // upload endpoint only takes file + name; apply the remaining settings afterwards
        if (cam.privacy_mode !== privacy || cam.enabled !== enabled || cam.target_fps !== fps) {
          cam = await api.updateCamera(cam.id, { privacy_mode: privacy, enabled, target_fps: fps });
        }
      } else {
        cam = await api.createCamera({
          name: name.trim(),
          source_type: type,
          source_uri: uri.trim(),
          enabled,
          privacy_mode: privacy,
          target_fps: fps,
        });
      }
      toast.success(`Camera "${cam.name}" added`);
      onCreated(cam);
      setOpen(false);
      reset();
    } catch (err) {
      toast.error("Could not add camera", { description: errorMessage(err) });
    } finally {
      setPending(false);
      setProgress(null);
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        if (!o) reset();
      }}
    >
      <DialogTrigger asChild>
        <Button>
          <Plus /> Add camera
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add camera</DialogTitle>
          <DialogDescription>Connect an RTSP stream, a local webcam or a video file.</DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="grid gap-4" noValidate>
          <Field id="cam-name" label="Name" error={errors.name}>
            <Input id="cam-name" value={name} onChange={(e) => setName(e.target.value)} aria-invalid={!!errors.name} placeholder="Loading dock" />
          </Field>
          <Field id="cam-type" label="Source type">
            <NativeSelect
              id="cam-type"
              value={type}
              onChange={(e) => {
                const t = e.target.value as SourceType;
                setType(t);
                setUri(t === "webcam" ? "0" : t === "file" ? (samples?.[0]?.source_uri ?? "") : "");
              }}
            >
              <option value="rtsp">RTSP stream</option>
              <option value="webcam">Webcam</option>
              <option value="file">Video file</option>
            </NativeSelect>
          </Field>

          {type === "rtsp" && (
            <Field id="cam-uri" label="RTSP URL" error={errors.uri}>
              <Input id="cam-uri" value={uri} onChange={(e) => setUri(e.target.value)} placeholder="rtsp://user:pass@host:554/stream" aria-invalid={!!errors.uri} />
            </Field>
          )}
          {type === "webcam" && (
            <Field id="cam-uri" label="Webcam index" error={errors.uri}>
              <Input id="cam-uri" inputMode="numeric" value={uri} onChange={(e) => setUri(e.target.value)} aria-invalid={!!errors.uri} />
            </Field>
          )}
          {type === "file" && (
            <fieldset className="grid gap-3">
              <legend className="mb-2 text-sm font-medium">Video</legend>
              <div className="flex gap-2" role="radiogroup" aria-label="Video source">
                {(["sample", "upload"] as const).map((m) => (
                  <Button
                    key={m}
                    type="button"
                    role="radio"
                    aria-checked={fileMode === m}
                    variant={fileMode === m ? "secondary" : "outline"}
                    size="sm"
                    onClick={() => setFileMode(m)}
                  >
                    {m === "sample" ? "Sample video" : "Upload video"}
                  </Button>
                ))}
              </div>
              {fileMode === "sample" ? (
                <Field id="cam-sample" label="Sample" error={errors.uri}>
                  <NativeSelect id="cam-sample" value={uri} onChange={(e) => setUri(e.target.value)} disabled={!samples?.length}>
                    {!samples ? <option value="">Loading…</option> : null}
                    {samples && !samples.length ? <option value="">No samples available</option> : null}
                    {samples?.map((s) => (
                      <option key={s.source_uri} value={s.source_uri}>
                        {s.name} ({formatBytes(s.size_bytes)})
                      </option>
                    ))}
                  </NativeSelect>
                </Field>
              ) : (
                <Field id="cam-file" label="Video file (max 200 MB)" error={errors.file}>
                  <Input id="cam-file" type="file" accept="video/*" onChange={(e) => setFile(e.target.files?.[0] ?? null)} aria-invalid={!!errors.file} />
                </Field>
              )}
              {progress !== null && (
                <div className="grid gap-1">
                  <Progress value={progress} label="Upload progress" />
                  <p className="flex items-center gap-1 text-xs text-muted-foreground">
                    <Upload className="size-3" /> Uploading… {progress}%
                  </p>
                </div>
              )}
            </fieldset>
          )}

          <Field id="cam-fps" label="Target FPS" error={errors.fps}>
            <Input id="cam-fps" type="number" min={1} max={60} value={fps} onChange={(e) => setFps(Number(e.target.value))} />
          </Field>
          <SwitchRow>
            <Label htmlFor="cam-privacy">Privacy mode (blur faces in snapshots)</Label>
            <Switch id="cam-privacy" checked={privacy} onCheckedChange={setPrivacy} />
          </SwitchRow>
          <SwitchRow>
            <Label htmlFor="cam-enabled">Enabled</Label>
            <Switch id="cam-enabled" checked={enabled} onCheckedChange={setEnabled} />
          </SwitchRow>
          <DialogFooter>
            <Button type="submit" disabled={pending}>
              {pending ? <Loader2 className="animate-spin" /> : null}
              Add camera
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
