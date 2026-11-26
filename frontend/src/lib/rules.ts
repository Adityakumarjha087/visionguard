import type { RuleParams, RuleType, Severity, Zone, ZoneKind } from "./types";

export const RULE_TYPES: RuleType[] = ["zone_intrusion", "dwell_time", "missing_ppe", "crowd_count", "line_crossing"];
export const SEVERITIES: Severity[] = ["low", "medium", "high", "critical"];
export const VEHICLE_CLASSES = ["car", "truck", "bus", "motorcycle", "vehicle"];

export const RULE_LABELS: Record<RuleType, string> = {
  zone_intrusion: "Zone intrusion",
  dwell_time: "Dwell time",
  missing_ppe: "Missing PPE",
  crowd_count: "Crowd count",
  line_crossing: "Line crossing",
};

export interface RuleTypeSpec {
  zone: "required" | "optional" | "none";
  zoneKind: ZoneKind;
  fields: ("classes" | "seconds" | "max_count")[];
  secondsLabel?: string;
  defaults: RuleParams;
}

export const RULE_SPECS: Record<RuleType, RuleTypeSpec> = {
  zone_intrusion: { zone: "required", zoneKind: "polygon", fields: ["classes"], defaults: { classes: ["person"] } },
  dwell_time: {
    zone: "required",
    zoneKind: "polygon",
    fields: ["seconds", "classes"],
    secondsLabel: "Stationary seconds",
    defaults: { seconds: 120, classes: [...VEHICLE_CLASSES] },
  },
  missing_ppe: {
    zone: "optional",
    zoneKind: "polygon",
    fields: ["seconds"],
    secondsLabel: "Grace seconds",
    defaults: { seconds: 3 },
  },
  crowd_count: { zone: "optional", zoneKind: "polygon", fields: ["max_count"], defaults: { max_count: 5 } },
  line_crossing: { zone: "required", zoneKind: "line", fields: [], defaults: {} },
};

export function defaultParams(type: RuleType): RuleParams {
  const d = RULE_SPECS[type].defaults;
  return { ...d, ...(d.classes ? { classes: [...d.classes] } : {}), cooldown_seconds: 30 };
}

export function zonesForType(type: RuleType, zones: Zone[]): Zone[] {
  const spec = RULE_SPECS[type];
  if (spec.zone === "none") return [];
  return zones.filter((z) => z.kind === spec.zoneKind);
}

export function parseClasses(s: string): string[] {
  return Array.from(new Set(s.split(/[,\s]+/).map((c) => c.trim().toLowerCase()).filter(Boolean)));
}

/** Keep only the params relevant to the type (plus cooldown). */
export function sanitizeParams(type: RuleType, p: RuleParams): RuleParams {
  const spec = RULE_SPECS[type];
  const out: RuleParams = {};
  if (spec.fields.includes("classes") && p.classes?.length) out.classes = p.classes;
  if (spec.fields.includes("seconds") && p.seconds !== undefined) out.seconds = p.seconds;
  if (spec.fields.includes("max_count") && p.max_count !== undefined) out.max_count = p.max_count;
  if (p.cooldown_seconds !== undefined) out.cooldown_seconds = p.cooldown_seconds;
  return out;
}

export interface RuleDraft {
  name: string;
  type: RuleType;
  zone_id: string | null;
  params: RuleParams;
  severity: Severity;
  enabled: boolean;
  notify_email: boolean;
  notify_telegram: boolean;
}

export function validateRule(d: RuleDraft, zones: Zone[]): Record<string, string> {
  const errors: Record<string, string> = {};
  const spec = RULE_SPECS[d.type];
  if (!d.name.trim()) errors.name = "Name is required";
  if (spec.zone === "required") {
    const z = zones.find((x) => x.id === d.zone_id);
    if (!z) errors.zone_id = `Select a ${spec.zoneKind} zone`;
    else if (z.kind !== spec.zoneKind) errors.zone_id = `Zone must be a ${spec.zoneKind}`;
  }
  if (spec.fields.includes("seconds") && !(Number(d.params.seconds) >= 0)) errors.seconds = "Must be >= 0";
  if (spec.fields.includes("max_count") && !(Number(d.params.max_count) >= 0)) errors.max_count = "Must be >= 0";
  if (spec.fields.includes("classes") && !d.params.classes?.length) errors.classes = "At least one class";
  return errors;
}
