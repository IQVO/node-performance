import type { ReactNode } from "react";
import { DataTable, StatusPill } from "@warehouse/ui-kit";
import { minutes, pct } from "../lib";
import type { Grade, Metrics } from "../types";

export function PageHeader({ title, subtitle }: { title: string; subtitle: ReactNode }) {
  return (
    <div>
      <h1 style={{ fontSize: "var(--wh-font-size-2xl)", margin: 0 }}>{title}</h1>
      <p style={{ color: "var(--wh-color-text-muted)", marginTop: 4 }}>{subtitle}</p>
    </div>
  );
}

export function Stack({ children, gap = 4 }: { children: ReactNode; gap?: 3 | 4 | 5 }) {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: `var(--wh-space-${gap})` }}>
      {children}
    </div>
  );
}

/** A label/value list (dl) for a record's scalar fields. */
export function Facts({ items }: { items: { label: string; value: ReactNode }[] }) {
  return (
    <dl
      style={{
        display: "grid",
        gridTemplateColumns: "max-content 1fr",
        gap: "var(--wh-space-2) var(--wh-space-4)",
        margin: 0,
        fontSize: "var(--wh-font-size-sm)",
      }}
    >
      {items.map((item) => (
        <div key={item.label} style={{ display: "contents" }}>
          <dt style={{ color: "var(--wh-color-text-muted)" }}>{item.label}</dt>
          <dd style={{ margin: 0 }}>{item.value}</dd>
        </div>
      ))}
    </dl>
  );
}

type Tone = "neutral" | "progress" | "success" | "warning" | "danger";

/** A grade's colour: A is best, D worst; PROVISIONAL is not a grade yet. */
const GRADE_TONE: Record<Grade, Tone> = {
  A: "success",
  B: "progress",
  C: "warning",
  D: "danger",
  PROVISIONAL: "neutral",
};

export function GradePill({ grade }: { grade: Grade }) {
  return <StatusPill status={grade} tone={GRADE_TONE[grade]} size="sm" />;
}

/** The metrics of one window, one row per metric, with the direction that is
 *  good. A metric whose denominator is empty reports its best value (the
 *  contract's rule), so an all-zeros window with observations is honest. */
export function MetricsTable({ metrics }: { metrics: Metrics }) {
  const rows: { key: string; metric: string; value: string; good: string }[] = [
    { key: "onTime", metric: "On-time handover", value: pct(metrics.onTimeHandoverRate), good: "higher is better" },
    { key: "lateAck", metric: "Late acknowledgements", value: pct(metrics.lateAckRate), good: "lower is better" },
    { key: "reject", metric: "Rejections", value: pct(metrics.rejectRate), good: "lower is better" },
    { key: "expiry", metric: "Expiries", value: pct(metrics.expiryRate), good: "lower is better" },
    { key: "promise", metric: "Promises kept", value: pct(metrics.promiseKeptRate), good: "higher is better" },
    { key: "freshness", metric: "Feed freshness (p95)", value: minutes(metrics.feedFreshnessP95Minutes), good: "lower is better" },
    { key: "observations", metric: "Observations", value: String(metrics.observations), good: "" },
  ];
  return (
    <DataTable
      rowKey={(r) => r.key}
      rows={rows}
      columns={[
        { key: "metric", header: "Metric", render: (r) => r.metric },
        { key: "value", header: "Value", align: "right", render: (r) => r.value },
        { key: "good", header: "", render: (r) => r.good },
      ]}
    />
  );
}
