import { useMemo } from "react";
import type { Session } from "../api/types";
import { controllable, sessionStale } from "../api/types";
import type { ListColumn } from "../components/DataList";
import { DataList } from "../components/DataList";
import { Status } from "../components/Status";
import { Button } from "../components/ui/button";
import { Checkbox } from "../components/ui/checkbox";
export function Workspaces({
  sessions,
  selected,
  onSelect,
  onOpen,
  now,
}: {
  sessions: Session[];
  selected: Set<string>;
  onSelect: (id: string, value: boolean) => void;
  onOpen: (id: string) => void;
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
          <div className="record">
            <Button
              variant="link"
              onClick={() => onOpen(row.original.identity.session_id)}
            >
              {row.original.root}
            </Button>
            <span className="caption mono">
              {row.original.identity.session_id}
            </span>
            <span className="caption">
              {row.original.terminal ?? "Foreground session"} · PID{" "}
              {row.original.pid}
              {row.original.tty ? ` · ${row.original.tty}` : ""}
            </span>
          </div>
        ),
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
    [selected, onSelect, onOpen, now],
  );
  return (
    <DataList
      data={sessions}
      columns={columns}
      label="workspaces"
      empty="No foreground sessions"
    />
  );
}
