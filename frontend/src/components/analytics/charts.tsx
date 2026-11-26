"use client";
import { Bar, BarChart, CartesianGrid, Legend, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import type { CountRow, CrossingRow } from "@/lib/analytics";
import { formatTime } from "@/lib/utils";

const axis = { stroke: "var(--muted-foreground)", fontSize: 12, tickLine: false, axisLine: false } as const;
const tooltipStyle = {
  contentStyle: {
    background: "var(--popover)",
    border: "1px solid var(--border)",
    borderRadius: 8,
    color: "var(--popover-foreground)",
    fontSize: 12,
  },
  labelStyle: { color: "var(--muted-foreground)" },
};

const tick = (v: string) => formatTime(v);
// legend text stays in the text colour; the swatch carries series identity
const legendText = (v: string) => <span style={{ color: "var(--foreground)" }}>{v}</span>;

export function CountsChart({ data }: { data: CountRow[] }) {
  return (
    <ResponsiveContainer width="100%" height={280}>
      <LineChart data={data} margin={{ top: 8, right: 16, left: -8, bottom: 0 }}>
        <CartesianGrid stroke="var(--border)" strokeDasharray="3 3" vertical={false} />
        <XAxis dataKey="bucket" tickFormatter={tick} minTickGap={32} {...axis} />
        <YAxis allowDecimals={false} {...axis} />
        <Tooltip {...tooltipStyle} labelFormatter={(l) => tick(String(l))} cursor={{ stroke: "var(--muted-foreground)", strokeDasharray: "3 3" }} />
        <Legend wrapperStyle={{ fontSize: 12 }} formatter={legendText} />
        <Line type="monotone" dataKey="people" name="People" stroke="var(--series-1)" strokeWidth={2} dot={false} activeDot={{ r: 4 }} />
        <Line type="monotone" dataKey="vehicles" name="Vehicles" stroke="var(--series-2)" strokeWidth={2} dot={false} activeDot={{ r: 4 }} />
      </LineChart>
    </ResponsiveContainer>
  );
}

export function CrossingsChart({ data }: { data: CrossingRow[] }) {
  return (
    <ResponsiveContainer width="100%" height={260}>
      <BarChart data={data} margin={{ top: 8, right: 16, left: -8, bottom: 0 }} barGap={2}>
        <CartesianGrid stroke="var(--border)" strokeDasharray="3 3" vertical={false} />
        <XAxis dataKey="bucket" tickFormatter={tick} minTickGap={24} {...axis} />
        <YAxis allowDecimals={false} {...axis} />
        <Tooltip {...tooltipStyle} labelFormatter={(l) => tick(String(l))} cursor={{ fill: "var(--accent)" }} />
        <Legend wrapperStyle={{ fontSize: 12 }} formatter={legendText} />
        <Bar dataKey="in" name="In" fill="var(--series-1)" radius={[4, 4, 0, 0]} maxBarSize={28} />
        <Bar dataKey="out" name="Out" fill="var(--series-2)" radius={[4, 4, 0, 0]} maxBarSize={28} />
      </BarChart>
    </ResponsiveContainer>
  );
}

export function LatencyChart({ data }: { data: { name: string; latency: number; active: boolean }[] }) {
  return (
    <ResponsiveContainer width="100%" height={Math.max(160, data.length * 44)}>
      <BarChart data={data} layout="vertical" margin={{ top: 4, right: 24, left: 8, bottom: 0 }}>
        <CartesianGrid stroke="var(--border)" strokeDasharray="3 3" horizontal={false} />
        <XAxis type="number" unit=" ms" {...axis} />
        <YAxis type="category" dataKey="name" width={120} {...axis} />
        <Tooltip {...tooltipStyle} formatter={(v) => [`${v} ms`, "Latency"]} cursor={{ fill: "var(--accent)" }} />
        <Bar dataKey="latency" name="Latency" fill="var(--series-1)" radius={[0, 4, 4, 0]} maxBarSize={24} label={{ position: "right", fontSize: 11, fill: "var(--muted-foreground)" }} />
      </BarChart>
    </ResponsiveContainer>
  );
}
