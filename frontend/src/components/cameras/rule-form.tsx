"use client";
import { useState } from "react";
import { Loader2 } from "lucide-react";
import { Field, SwitchRow } from "@/components/common/field";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { NativeSelect } from "@/components/ui/native-select";
import { Switch } from "@/components/ui/switch";
import {
  RULE_LABELS,
  RULE_SPECS,
  RULE_TYPES,
  SEVERITIES,
  defaultParams,
  parseClasses,
  sanitizeParams,
  validateRule,
  zonesForType,
  type RuleDraft,
} from "@/lib/rules";
import type { Rule, RuleInput, RuleType, Severity, Zone } from "@/lib/types";

export function draftFromRule(r?: Rule): RuleDraft {
  if (!r) {
    return {
      name: "",
      type: "zone_intrusion",
      zone_id: null,
      params: defaultParams("zone_intrusion"),
      severity: "medium",
      enabled: true,
      notify_email: false,
      notify_telegram: false,
    };
  }
  return {
    name: r.name,
    type: r.type,
    zone_id: r.zone_id,
    params: { ...defaultParams(r.type), ...r.params },
    severity: r.severity,
    enabled: r.enabled,
    notify_email: r.notify_email,
    notify_telegram: r.notify_telegram,
  };
}

export function toRuleInput(d: RuleDraft): RuleInput {
  const spec = RULE_SPECS[d.type];
  return {
    name: d.name.trim(),
    type: d.type,
    zone_id: spec.zone === "none" ? null : d.zone_id || null,
    params: sanitizeParams(d.type, d.params),
    severity: d.severity,
    enabled: d.enabled,
    notify_email: d.notify_email,
    notify_telegram: d.notify_telegram,
  };
}

export interface RuleFormProps {
  zones: Zone[];
  initial?: Rule;
  onSubmit: (input: RuleInput) => Promise<void>;
  onCancel?: () => void;
}

export function RuleForm({ zones, initial, onSubmit, onCancel }: RuleFormProps) {
  const [d, setD] = useState<RuleDraft>(() => draftFromRule(initial));
  const [classesText, setClassesText] = useState(() => (d.params.classes ?? []).join(", "));
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [pending, setPending] = useState(false);
  const spec = RULE_SPECS[d.type];
  const eligibleZones = zonesForType(d.type, zones);

  function setType(type: RuleType) {
    const params = defaultParams(type);
    const nextZones = zonesForType(type, zones);
    setD((prev) => ({
      ...prev,
      type,
      params: { ...params, cooldown_seconds: prev.params.cooldown_seconds ?? params.cooldown_seconds },
      zone_id: nextZones.some((z) => z.id === prev.zone_id) ? prev.zone_id : RULE_SPECS[type].zone === "required" ? (nextZones[0]?.id ?? null) : null,
    }));
    setClassesText((params.classes ?? []).join(", "));
    setErrors({});
  }

  const setParam = (k: "seconds" | "max_count" | "cooldown_seconds", v: string) =>
    setD((prev) => ({ ...prev, params: { ...prev.params, [k]: v === "" ? undefined : Number(v) } }));

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    const draft: RuleDraft = { ...d, params: { ...d.params, classes: parseClasses(classesText) } };
    const v = validateRule(draft, zones);
    setErrors(v);
    if (Object.keys(v).length) return;
    setPending(true);
    try {
      await onSubmit(toRuleInput(draft));
    } finally {
      setPending(false);
    }
  }

  return (
    <form onSubmit={submit} className="grid gap-4" noValidate aria-label="Rule form">
      <Field id="rule-name" label="Name" error={errors.name}>
        <Input id="rule-name" value={d.name} onChange={(e) => setD({ ...d, name: e.target.value })} aria-invalid={!!errors.name} />
      </Field>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field id="rule-type" label="Type">
          <NativeSelect id="rule-type" value={d.type} onChange={(e) => setType(e.target.value as RuleType)}>
            {RULE_TYPES.map((t) => (
              <option key={t} value={t}>
                {RULE_LABELS[t]}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field id="rule-severity" label="Severity">
          <NativeSelect id="rule-severity" value={d.severity} onChange={(e) => setD({ ...d, severity: e.target.value as Severity })}>
            {SEVERITIES.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </NativeSelect>
        </Field>
      </div>

      {spec.zone !== "none" && (
        <Field
          id="rule-zone"
          label={spec.zone === "required" ? `Zone (${spec.zoneKind})` : `Zone (${spec.zoneKind}, optional)`}
          error={errors.zone_id}
          hint={!eligibleZones.length ? `No ${spec.zoneKind} zones yet - draw one in the Zones tab.` : undefined}
        >
          <NativeSelect
            id="rule-zone"
            value={d.zone_id ?? ""}
            onChange={(e) => setD({ ...d, zone_id: e.target.value || null })}
            aria-invalid={!!errors.zone_id}
          >
            <option value="">{spec.zone === "required" ? "Select a zone…" : "Whole frame"}</option>
            {eligibleZones.map((z) => (
              <option key={z.id} value={z.id}>
                {z.name}
              </option>
            ))}
          </NativeSelect>
        </Field>
      )}

      {spec.fields.includes("seconds") && (
        <Field id="rule-seconds" label={spec.secondsLabel ?? "Seconds"} error={errors.seconds}>
          <Input id="rule-seconds" type="number" min={0} value={d.params.seconds ?? ""} onChange={(e) => setParam("seconds", e.target.value)} />
        </Field>
      )}
      {spec.fields.includes("max_count") && (
        <Field id="rule-max" label="Max people (alert when count exceeds)" error={errors.max_count}>
          <Input id="rule-max" type="number" min={0} value={d.params.max_count ?? ""} onChange={(e) => setParam("max_count", e.target.value)} />
        </Field>
      )}
      {spec.fields.includes("classes") && (
        <Field id="rule-classes" label="Classes" error={errors.classes} hint="Comma-separated, e.g. person, car">
          <Input id="rule-classes" value={classesText} onChange={(e) => setClassesText(e.target.value)} />
        </Field>
      )}
      <Field id="rule-cooldown" label="Cooldown seconds">
        <Input
          id="rule-cooldown"
          type="number"
          min={0}
          value={d.params.cooldown_seconds ?? ""}
          onChange={(e) => setParam("cooldown_seconds", e.target.value)}
        />
      </Field>

      <div className="grid gap-2">
        <SwitchRow>
          <Label htmlFor="rule-enabled">Enabled</Label>
          <Switch id="rule-enabled" checked={d.enabled} onCheckedChange={(v) => setD({ ...d, enabled: v })} />
        </SwitchRow>
        <SwitchRow>
          <Label htmlFor="rule-email">Notify by email</Label>
          <Switch id="rule-email" checked={d.notify_email} onCheckedChange={(v) => setD({ ...d, notify_email: v })} />
        </SwitchRow>
        <SwitchRow>
          <Label htmlFor="rule-telegram">Notify via Telegram</Label>
          <Switch id="rule-telegram" checked={d.notify_telegram} onCheckedChange={(v) => setD({ ...d, notify_telegram: v })} />
        </SwitchRow>
      </div>
      <div className="flex justify-end gap-2">
        {onCancel ? (
          <Button variant="outline" onClick={onCancel}>
            Cancel
          </Button>
        ) : null}
        <Button type="submit" disabled={pending}>
          {pending ? <Loader2 className="animate-spin" /> : null}
          {initial ? "Save rule" : "Create rule"}
        </Button>
      </div>
    </form>
  );
}
