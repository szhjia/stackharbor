import type { Metric } from "../api/types";
export const memory = (value: number | null | undefined) =>
  value == null ? "Unknown" : `${(value / 1048576).toFixed(1)} MiB`;
export const cpu = (value: number | null | undefined) =>
  value == null ? "Unknown" : `${value.toFixed(1)}%`;
export function MetricView({ metric }: { metric: Metric }) {
  return (
    <div className="metrics">
      <span>
        <small>CPU</small> {cpu(metric.cpu_percent)}
      </span>
      <span>
        <small>RSS</small> {metric.known ? memory(metric.rss_bytes) : "Unknown"}
      </span>
      {metric.partial ? <small>Partial sample</small> : null}
    </div>
  );
}
