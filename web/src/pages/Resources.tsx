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
import { Status } from "../components/Status";
import { Button } from "../components/ui/button";
const resourceInfoCell: ListColumn<Resource>["cell"] = ({row}) => <ResourceInfo resource={row.original} kind="identity" />;
const sampleInfoCell: ListColumn<Resource>["cell"] = ({row}) => <ResourceInfo resource={row.original} kind="sample" />;
const OwnershipExpansion = createContext<{expanded: Set<string>; toggle: (key: string) => void}>({expanded: new Set(), toggle: () => {}});
function Ownership({resourceID, reference, root}: {resourceID: string; reference: Resource["references"][number]; root: string}) {
  const {t: tr} = usePreferences();
  const {expanded, toggle} = useContext(OwnershipExpansion);
  const key = JSON.stringify([resourceID, reference.session_id]);
  const open = expanded.has(key);
  const contentID = useId();
  const nodes = reference.nodes;
  return <div className="ownership">
    <div className="ownership-heading">
      <Button variant="link" asChild><Link title={root} to={workspacePath(reference.session_id)}>{root}</Link></Button>
      <Button variant="ghost" size="sm" className="ownership-toggle" aria-expanded={open} aria-controls={contentID}
        aria-label={tr("{action} {count} nodes for {root}", {action: tr(open ? "Hide" : "Show"), count: nodes.length, root})} onClick={() => toggle(key)}>
        {nodes.length} {nodes.length === 1 ? tr("node") : tr("nodes")}{open ? <ChevronDown /> : <ChevronRight />}
      </Button>
    </div>
    <div id={contentID} hidden={!open} className="ownership-nodes">
      {nodes.length === 0 ? <span className="caption">{tr("No node details available")}</span> : nodes.map((n) => <span key={n.id} className="ownership-node">
        <span className="mono">{n.id}</span><Status value={n.role} />
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
        id: "ownership",
        header: tr("Workspace / Nodes"),
        accessorFn: (r) =>
          r.references
            .map(
              (ref) => roots.get(ref.session_id) + " " + ref.node_ids.join(" "),
            )
            .join(" "),
        cell: ({ row }) => (
          <div className="ownership-list">
            {row.original.references.map((ref) => (
              <Ownership key={ref.session_id} resourceID={row.original.id} reference={ref} root={roots.get(ref.session_id) ?? ref.workspace_id} />
            ))}
          </div>
        ),
      },
      {id: "cpu", header: "CPU", cell: ({row}) => <MetricValue metric={row.original.metric} kind="cpu" />},
      {id: "memory", header: tr("Memory"), cell: ({row}) => <MetricValue metric={row.original.metric} kind="memory" />},
      {id: "sample", header: tr("Sample"), cell: sampleInfoCell},
    ],
    [sessions, tr],
  );
  return (
    <OwnershipExpansion.Provider value={{expanded, toggle}}>
    <DataList
      data={resources}
      columns={columns}
      label="resources"
      layout="resources"
      empty={tr("No observed resources")}
    />
    </OwnershipExpansion.Provider>
  );
}
