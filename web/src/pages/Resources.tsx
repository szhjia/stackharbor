import { usePreferences } from "../lib/preferences";
import { Link } from "react-router";
import { workspacePath } from "../routes";
import { createContext, useContext, useId, useMemo, useState } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import type { Resource, Session } from "../api/types";
import type { ListColumn } from "../components/DataList";
import { DataList } from "../components/DataList";
import { MetricValue } from "../components/MetricView";
import { ResourceInfo } from "../components/ResourceInfo";
import { WorkspaceLink } from "../components/WorkspaceLink";
import { Status } from "../components/Status";
import { Button } from "../components/ui/button";
import { useListQuery } from "../lib/list-state";
import { Select, SelectTrigger, SelectValue, SelectContent, SelectGroup, SelectItem } from "../components/ui/select";
const resourceInfoCell: ListColumn<Resource>["cell"] = ({row}) => <ResourceInfo resource={row.original} kind="identity" />;
const sampleInfoCell: ListColumn<Resource>["cell"] = ({row}) => <ResourceInfo resource={row.original} kind="sample" />;
const OwnershipExpansion = createContext<{expanded: Set<string>; toggle: (key: string) => void}>({expanded: new Set(), toggle: () => {}});
function OwnershipNodes({resourceID, reference, root, multiple}: {resourceID: string; reference: Resource["references"][number]; root: string; multiple: boolean}) {
  const {t: tr} = usePreferences();
  const {expanded, toggle} = useContext(OwnershipExpansion);
  const key = JSON.stringify([resourceID, reference.session_id]);
  const open = expanded.has(key);
  const contentID = useId();
  const nodes = reference.nodes;
  return <div className="ownership">
    <div className="ownership-heading">
      <Button variant="ghost" size="sm" className="ownership-toggle" data-multiple={multiple || undefined} title={root} aria-expanded={open} aria-controls={contentID}
        aria-label={tr("{action} {count} nodes for {root}", {action: tr(open ? "Hide" : "Show"), count: nodes.length, root})} onClick={() => toggle(key)}>
        {multiple ? <span className="ownership-owner">{root.split("/").filter(Boolean).at(-1) || root}</span> : null}
        <span className="ownership-count">{nodes.length} {nodes.length === 1 ? tr("node") : tr("nodes")}</span>{open ? <ChevronDown /> : <ChevronRight />}
      </Button>
    </div>
    <div id={contentID} hidden={!open} className="ownership-nodes">
      {multiple ? <WorkspaceLink sessionID={reference.session_id} root={root} /> : null}
      {nodes.length === 0 ? <span className="caption">{tr("No node details available")}</span> : nodes.map((n) => <span key={n.id} className="ownership-node">
        <Link className="mono" aria-label={tr("Logs for {name}", {name: n.id})} to={workspacePath(reference.session_id, "logs", n.id)}>{n.id}</Link><Status value={n.role} />
        <span className="caption">{tr(n.ownership)} · {tr(n.state)}</span>
      </span>)}
    </div>
  </div>;
}
export function Resources({
  resources,
  sessions,
}: {
  resources: Resource[];
  sessions: Session[];
}) {
  const {t: tr} = usePreferences();
  const roots = new Map(sessions.map((s) => [s.identity.session_id, s.root]));
  const {sort, kind, update} = useListQuery();
  const visible = resources.filter(resource => !["process", "container"].includes(kind) || resource.kind === kind);
  if (sort === "memory" || sort === "cpu") {
    const value = (resource: Resource) => {
      const sample = sort === "memory" ? resource.metric.known ? resource.metric.rss_bytes : null : resource.metric.cpu_percent;
      return sample != null && Number.isFinite(sample) && sample >= 0 ? sample : -1;
    };
    visible.sort((a, b) => value(b) - value(a) || a.id.localeCompare(b.id));
  }
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set());
  const toggle = (key: string) => setExpanded((previous) => {
    const next = new Set(previous);
    if (next.has(key)) next.delete(key); else next.add(key);
    return next;
  });
  const columns = useMemo<ListColumn<Resource>[]>(
    () => [
      {accessorKey: "kind", header: tr("Type"), cell: ({row}) => <span>{row.original.kind === "process" ? tr("Process") : row.original.kind === "container" ? tr("Container") : row.original.kind}</span>},
      {
        id: "identity",
        accessorFn: (r) => `${r.id} ${r.pid ?? ""} ${r.container_id ?? ""}`,
        header: tr("PID / ID"),
        cell: resourceInfoCell,
      },
      {
        id: "workspace",
        header: tr("Workspace"),
        accessorFn: (r) =>
          r.references
            .map(
              (ref) => roots.get(ref.session_id) + " " + ref.node_ids.join(" "),
            )
            .join(" "),
        cell: ({ row }) => (
          <div className="workspace-references">
            {row.original.references.length ? row.original.references.map(ref => <WorkspaceLink key={ref.session_id} sessionID={ref.session_id} root={roots.get(ref.session_id) ?? ref.workspace_id} />) : <span className="caption">{tr("Unknown")}</span>}
          </div>
        ),
      },
      {id: "nodes", header: tr("Nodes"), cell: ({row}) => <div className="ownership-list">
        {row.original.references.length ? row.original.references.map(ref => <OwnershipNodes key={ref.session_id} resourceID={row.original.id} reference={ref} root={roots.get(ref.session_id) ?? ref.workspace_id} multiple={row.original.references.length > 1} />) : <span className="caption">{tr("Unknown")}</span>}
      </div>},
      {id: "cpu", header: "CPU", cell: ({row}) => <MetricValue metric={row.original.metric} kind="cpu" />},
      {id: "memory", header: tr("Memory"), cell: ({row}) => <MetricValue metric={row.original.metric} kind="memory" />},
      {id: "sample", header: tr("Sample"), cell: sampleInfoCell},
    ],
    [sessions, tr],
  );
  return (
    <OwnershipExpansion.Provider value={{expanded, toggle}}>
    <DataList
      data={visible}
      columns={columns}
      label="resources"
      layout="resources"
      empty={tr("No observed resources")}
      emptyDescription={tr("Observed processes and containers appear here. Try another resource type if this list is empty.")}
      searchText={r => [r.id, r.pid, r.container_id, r.kind, tr(r.kind === "process" ? "Process" : "Container"), ...r.references.flatMap(ref => [roots.get(ref.session_id) ?? ref.workspace_id, ref.session_id, ...ref.node_ids])].join(" ")}
      toolbar={<>
        <Select value={["process", "container"].includes(kind) ? kind : "all"} onValueChange={value => update("kind", value)}>
          <SelectTrigger aria-label={tr("Resource type")}><SelectValue /></SelectTrigger>
          <SelectContent><SelectGroup>{[["all", "All resource types"], ["process", "Process"], ["container", "Container"]].map(([value, label]) => <SelectItem value={value} key={value}>{tr(label)}</SelectItem>)}</SelectGroup></SelectContent>
        </Select>
        <Select value={["memory", "cpu"].includes(sort) ? sort : "default"} onValueChange={value => update("sort", value)}>
          <SelectTrigger aria-label={tr("Sort resources")}><SelectValue /></SelectTrigger>
          <SelectContent><SelectGroup>{[["default", "Default order"], ["memory", "Memory: highest first"], ["cpu", "CPU: highest first"]].map(([value, label]) => <SelectItem value={value} key={value}>{tr(label)}</SelectItem>)}</SelectGroup></SelectContent>
        </Select>
      </>}
    />
    </OwnershipExpansion.Provider>
  );
}
