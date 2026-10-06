import type { Node } from "../api/types";
import { Status } from "./Status";
import { InfoPopover } from "./InfoPopover";
import { DefinitionList, type DefinitionItem } from "./DefinitionList";

export function NodeInfo({node, kind}: {node: Node; kind: "identity" | "state" | "ports"}) {
  const title = kind === "identity" ? "Node details" : kind === "state" ? "State details" : "Port details";
  const items: DefinitionItem[] = kind === "identity" ? [
    {label: "Node ID", value: node.id, mono: true},
    {label: "Ownership", value: node.ownership},
    {label: "Dependencies", value: node.depends_on.length ? node.depends_on.join(", ") : "None"},
    {label: `Resources · ${node.resource_refs.length}`, value: node.resource_refs.length ? node.resource_refs.map(ref => <div className="mono" key={ref}>{ref}</div>) : "None"},
  ] : kind === "state" ? [
    {label: "State", value: node.state},
    {label: "Reason", value: node.reason || "No additional diagnostics"},
    {label: "Metrics", value: node.metric.partial ? "Partial sample" : node.metric.known ? "Sampled" : "Unknown"},
  ] : node.ports.length ? node.ports.map(port => ({
    label: `Port ${port.port}`, value: <>{port.status}{port.reason ? ` · ${port.reason}` : ""}</>,
  })) : [{label: "Ports", value: "No declared ports"}];
  return <InfoPopover title={title} label={`${title}: ${node.id}`} className="node-info-trigger"
    summary={kind === "identity" ? <span className="truncate">{node.name || node.id}</span> : kind === "state" ? <Status value={node.state} /> : <span className="mono truncate">{node.ports.length ? node.ports.map(port => `:${port.port}`).join(", ") : "—"}</span>}>
    <DefinitionList items={items} />
  </InfoPopover>;
}
