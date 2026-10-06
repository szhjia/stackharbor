import { Link } from "react-router";
import { workspacePath } from "../routes";
import { useMemo } from "react";
import type { Session } from "../api/types";
import { controllable, sessionStale } from "../api/types";
import type { ListColumn } from "../components/DataList";
import { DataList } from "../components/DataList";
import { Status } from "../components/Status";
import { Button } from "../components/ui/button";
import { Checkbox } from "../components/ui/checkbox";
import { WorkspaceInfo } from "../components/WorkspaceInfo";
const sessionInfoCell: ListColumn<Session>["cell"] = ({row}) => <WorkspaceInfo session={row.original} kind="session" />;
const terminalInfoCell: ListColumn<Session>["cell"] = ({row}) => <WorkspaceInfo session={row.original} kind="terminal" />;
export function Workspaces({
  sessions,
  selected,
  onSelect,
  now,
}: {
  sessions: Session[];
  selected: Set<string>;
  onSelect: (id: string, value: boolean) => void;
  now: number;
}) {
  const columns = useMemo<ListColumn<Session>[]>(
    () => [
      {
        id: "select",
        header: "Select",
        cell: ({ row }) => (
          <Checkbox
            aria-label={`Select ${row.original.root}`}
            disabled={!controllable(row.original, now)}
            checked={selected.has(row.original.identity.session_id)}
            onCheckedChange={(v) =>
              onSelect(row.original.identity.session_id, v === true)
            }
          />
        ),
      },
      {
        accessorKey: "root",
        header: "Workspace",
        cell: ({ row }) => (
          <Button variant="link" asChild>
            <Link className="workspace-root" title={row.original.root} to={workspacePath(row.original.identity.session_id)}>{row.original.root}</Link>
          </Button>
        ),
      },
      {
        accessorFn: (s) => s.identity.session_id,
        id: "session",
        header: "Session",
        cell: sessionInfoCell,
      },
      {accessorKey: "pid", header: "PID", cell: ({row}) => <span className="workspace-pid mono">{row.original.pid}</span>},
      {
        id: "terminal",
        header: "Terminal",
        accessorFn: (s) => `${s.terminal ?? "Foreground session"} ${s.tty ?? ""}`,
        cell: terminalInfoCell,
      },
      {
        id: "state",
        header: "Connection",
        cell: ({ row }) => (
          <div className="record">
            <Status
              value={
                !row.original.available
                  ? "unavailable"
                  : sessionStale(row.original, now)
                    ? "stale"
                    : row.original.identity.protocol_version !== 1
                      ? "unsupported"
                      : "Live"
              }
            />
            {row.original.error ? (
              <span className="caption">{row.original.error.message}</span>
            ) : null}
          </div>
        ),
      },
      {
        id: "nodes",
        header: "Nodes",
        accessorFn: (s) => s.snapshot?.nodes.length ?? "Unknown",
      },
      {
        id: "observed",
        header: "Last observation",
        cell: ({ row }) => (
          <span className="caption">
            {row.original.snapshot
              ? new Date(row.original.snapshot.observed_at).toLocaleTimeString()
              : "Unknown"}
          </span>
        ),
      },
    ],
    [selected, onSelect, now],
  );
  return (
    <DataList
      data={sessions}
      columns={columns}
      label="workspaces"
      layout="workspaces"
      empty="No foreground sessions"
    />
  );
}
