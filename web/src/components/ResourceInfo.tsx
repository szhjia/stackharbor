import { usePreferences } from "../lib/preferences";
import type { Resource } from "../api/types";
import { InfoPopover } from "./InfoPopover";
import { DefinitionList, type DefinitionItem } from "./DefinitionList";

export function ResourceInfo({resource, kind}: {resource: Resource; kind: "identity" | "sample"}) {
  const {t: tr, language} = usePreferences();
  const summary = kind === "identity" ? resource.kind === "process" ? String(resource.pid ?? tr("Unknown")) : resource.container_id?.slice(0, 12) || tr("Unknown")
    : resource.metric.partial ? tr("Partial") : resource.metric.known ? tr("Sampled") : tr("Unknown");
  const items: DefinitionItem[] = kind === "identity" ? [
    {label: tr("Resource ID"), value: resource.id, mono: true},
    {label: tr("Identity"), value: resource.identity_known ? tr("Known") : tr("Unknown")},
    ...(resource.container_id ? [{label: tr("Container ID"), value: resource.container_id, mono: true}] : []),
    ...(resource.endpoint_identity ? [{label: tr("Endpoint"), value: resource.endpoint_identity}] : []),
  ] : [
    {label: tr("Coverage"), value: resource.metric.partial ? tr("Partial sample") : resource.metric.known ? tr("Complete sample") : tr("Unknown")},
    {label: tr("Memory"), value: resource.metric.known && resource.metric.rss_bytes != null ? tr("Available") : tr("Unknown")},
    {label: tr("Sampled at"), value: resource.metric.sampled_at ? new Date(resource.metric.sampled_at).toLocaleString(language) : tr("Unknown")},
  ];
  const title = kind === "identity" ? tr("Resource details") : tr("Sample details");
  return <InfoPopover title={title} label={`${title}: ${resource.id}`}
    className={`resource-info-trigger ${kind === "identity" ? "mono" : ""}`}
    summary={<span className="truncate">{summary}</span>}>
    <DefinitionList items={items} />
  </InfoPopover>;
}
