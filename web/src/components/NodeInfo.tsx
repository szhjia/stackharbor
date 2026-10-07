import { usePreferences } from "../lib/preferences";
import type { Node } from "../api/types";
import { Status } from "./Status";
import { InfoPopover } from "./InfoPopover";
import { DefinitionList, type DefinitionItem } from "./DefinitionList";

export function NodeInfo({node, kind}: {node: Node; kind: "identity" | "state" | "ports"}) {
  const {t: tr} = usePreferences();
  const title = kind === "identity" ? tr("Node details") : kind === "state" ? tr("State details") : tr("Port details");
  const items: DefinitionItem[] = kind === "identity" ? [
    {label: tr("Node ID"), value: node.id, mono: true},
    {label: tr("Ownership"), value: tr(node.ownership)},
    {label: tr("Dependencies"), value: node.depends_on.length ? node.depends_on.join(", ") : tr("None")},
    {label: tr("Resources · {count}", {count: node.resource_refs.length}), value: node.resource_refs.length ? node.resource_refs.map(ref => <div className="mono" key={ref}>{ref}</div>) : tr("None")},
  ] : kind === "state" ? [
    {label: tr("State"), value: tr(node.state)},
    {label: tr("Reason"), value: node.reason || tr("No additional diagnostics")},
    {label: tr("Metrics"), value: node.metric.partial ? tr("Partial sample") : node.metric.known ? tr("Sampled") : tr("Unknown")},
  ] : node.ports.length ? node.ports.map(port => ({
    label: tr("Port {port}", {port: port.port}), value: <>{tr(port.status)}{port.reason ? ` · ${port.reason}` : ""}</>,
  })) : [{label: tr("Ports"), value: tr("No declared ports")}];
  return <InfoPopover title={title} label={`${title}: ${node.id}`} className="node-info-trigger"
    summary={kind === "identity" ? <span className="truncate">{node.name || node.id}</span> : kind === "state" ? <Status value={node.state} /> : <span className="mono truncate">{node.ports.length ? node.ports.map(port => `:${port.port}`).join(", ") : "—"}</span>}>
    <DefinitionList items={items} />
  </InfoPopover>;
}
