import type { Resource } from "../api/types";
import { InfoPopover } from "./InfoPopover";
import { DefinitionList, type DefinitionItem } from "./DefinitionList";

export function ResourceInfo({resource, kind}: {resource: Resource; kind: "identity" | "sample"}) {
  const summary = kind === "identity" ? resource.kind === "process" ? String(resource.pid ?? "Unknown") : resource.container_id?.slice(0, 12) || "Unknown"
    : resource.metric.partial ? "Partial" : resource.metric.known ? "Sampled" : "Unknown";
  const items: DefinitionItem[] = kind === "identity" ? [
    {label: "Resource ID", value: resource.id, mono: true},
    {label: "Identity", value: resource.identity_known ? "Known" : "Unknown"},
    ...(resource.container_id ? [{label: "Container ID", value: resource.container_id, mono: true}] : []),
    ...(resource.endpoint_identity ? [{label: "Endpoint", value: resource.endpoint_identity}] : []),
  ] : [
    {label: "Coverage", value: resource.metric.partial ? "Partial sample" : resource.metric.known ? "Complete sample" : "Unknown"},
    {label: "Memory", value: resource.metric.known && resource.metric.rss_bytes != null ? "Available" : "Unknown"},
    {label: "Sampled at", value: resource.metric.sampled_at ? new Date(resource.metric.sampled_at).toLocaleString() : "Unknown"},
  ];
  const title = kind === "identity" ? "Resource details" : "Sample details";
  return <InfoPopover title={title} label={`${title}: ${resource.id}`}
    className={`resource-info-trigger ${kind === "identity" ? "mono" : ""}`}
    summary={<span className="truncate">{summary}</span>}>
    <DefinitionList items={items} />
  </InfoPopover>;
}
