import { useMemo } from "react";
import type { Resource, Session } from "../api/types";
import type { ListColumn } from "../components/DataList";
import { DataList } from "../components/DataList";
import { MetricView } from "../components/MetricView";
import { Status } from "../components/Status";
import { Button } from "../components/ui/button";
export function Resources({
  resources,
  sessions,
  onOpen,
}: {
  resources: Resource[];
  sessions: Session[];
  onOpen: (sid: string) => void;
}) {
  const roots = new Map(sessions.map((s) => [s.identity.session_id, s.root]));
  const columns = useMemo<ListColumn<Resource>[]>(
    () => [
      {
        accessorKey: "id",
        header: "Physical resource",
        cell: ({ row }) => (
          <div className="record">
            <strong>
              {row.original.kind === "process"
                ? `PID ${row.original.pid ?? "unknown"}`
                : row.original.container_id?.slice(0, 12) ||
                  "Unknown container"}
            </strong>
            <span className="mono caption">{row.original.id}</span>
            {!row.original.identity_known ? (
              <Status value="identity unknown" />
            ) : null}
          </div>
        ),
      },
      {
        id: "ownership",
        header: "Workspace → node ownership",
        accessorFn: (r) =>
          r.references
            .map(
              (ref) => roots.get(ref.session_id) + " " + ref.node_ids.join(" "),
            )
            .join(" "),
        cell: ({ row }) => (
          <div className="ownership-list">
            {row.original.references.map((ref) => (
              <div className="ownership" key={ref.session_id}>
                <Button variant="link" onClick={() => onOpen(ref.session_id)}>
                  {roots.get(ref.session_id) ?? ref.workspace_id}
                </Button>
                {ref.nodes.map((n) => (
                  <span key={n.id} className="ownership-node">
                    <span className="mono">{n.id}</span>
                    <Status value={n.role} />
                    <span className="caption">
                      {n.ownership} · {n.state}
                    </span>
                  </span>
                ))}
              </div>
            ))}
          </div>
        ),
      },
      {
        id: "metrics",
        header: "Observation",
        cell: ({ row }) => <MetricView metric={row.original.metric} />,
      },
    ],
    [sessions, onOpen],
  );
  return (
    <DataList
      data={resources}
      columns={columns}
      label="resources"
      empty="No observed resources"
    />
  );
}
