import { usePreferences } from "../lib/preferences";
import type { Metric } from "../api/types";
export const memory = (value: number | null | undefined) =>
  value == null ? "Unknown" : `${(value / 1048576).toFixed(1)} MiB`;
export const cpu = (value: number | null | undefined) =>
  value == null ? "Unknown" : `${value.toFixed(1)}%`;
export function MetricView({ metric }: { metric: Metric }) {
  const {t: tr} = usePreferences();
  return (
    <div className="metrics">
      <span>
        <small>{tr("CPU")}</small> {tr(cpu(metric.cpu_percent))}
      </span>
      <span>
        <small>{tr("RSS")}</small> {metric.known ? tr(memory(metric.rss_bytes)) : tr("Unknown")}
      </span>
      {metric.partial ? <small>{tr("Partial sample")}</small> : null}
    </div>
  );
}

export function MetricValue({metric, kind}: {metric: Metric; kind: "cpu" | "memory"}) {
  const {t: tr} = usePreferences();
  return <span className="resource-metric mono">{kind === "cpu" ? tr(cpu(metric.cpu_percent)) : metric.known ? tr(memory(metric.rss_bytes)) : tr("Unknown")}</span>;
}
