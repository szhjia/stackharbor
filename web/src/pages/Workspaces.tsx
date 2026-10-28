import { usePreferences } from "../lib/preferences";
import { useMemo } from "react";
import type { Session } from "../api/types";
import { sessionStale } from "../api/types";
import type { ListColumn } from "../components/DataList";
import { DataList } from "../components/DataList";
import { Status } from "../components/Status";
import { WorkspaceInfo } from "../components/WorkspaceInfo";
import { WorkspaceLink } from "../components/WorkspaceLink";
import { summarizeWorkspaceNodes } from "../lib/workspace-summary";
type WorkspaceRow = Session & {summary: ReturnType<typeof summarizeWorkspaceNodes>};
const sessionInfoCell: ListColumn<WorkspaceRow>["cell"] = ({row}) => <WorkspaceInfo session={row.original} />;
const connectionState = (s: Session, now: number) =>
  !s.available ? "unavailable" : sessionStale(s, now) ? "stale" : s.identity.protocol_version !== 1 ? "unsupported" : "Live";
export function Workspaces({
  sessions,
  now,
}: {
  sessions: Session[];
  now: number;
}) {
  const {t: tr, language} = usePreferences();
  const rows = useMemo(() => sessions.map(session => ({...session, summary: summarizeWorkspaceNodes(session.snapshot?.nodes)})), [sessions]);
  const columns = useMemo<ListColumn<WorkspaceRow>[]>(
    () => [
      {
        accessorFn: (s) => s.root,
        id: "root",
        header: tr("Workspace"),
        cell: ({ row }) => <WorkspaceLink sessionID={row.original.identity.session_id} root={row.original.root} />,
      },
      {
        id: "terminal", header: tr("Terminal"),
        cell: ({row}) => <span className="table-text" title={row.original.tty}>{row.original.terminal || tr("Foreground session")}</span>,
      },
      {
        accessorKey: "pid", header: "PID",
        cell: ({row}) => <span className="mono">{row.original.pid}</span>,
      },
      {
        accessorFn: (s) => s.identity.session_id,
        id: "session",
        header: tr("Session"),
        cell: sessionInfoCell,
      },
      {
        id: "state",
        header: tr("Connection"),
        accessorFn: (s) => connectionState(s, now),
        cell: ({ row }) => (
          <span title={row.original.error?.message}>
            <Status value={connectionState(row.original, now)} />
          </span>
        ),
      },
      ...([['services', 'Service / resource nodes'], ['tasks', 'Tasks'], ['runningServices', 'Ready services'], ['abnormal', 'Abnormal nodes']] as const).map(([id, label]) => ({
        id, header: tr(label),
        cell: ({row}: {row: {original: WorkspaceRow}}) => <span className="mono table-count" data-attention={(id === "abnormal" && Boolean(row.original.summary?.abnormal)) || undefined}>{row.original.summary?.[id] ?? tr("Unknown")}</span>,
      })),
      {
        id: "observed",
        header: tr("Last observation"),
        cell: ({ row }) => (
          <span className="caption">
            {row.original.snapshot
              ? new Date(row.original.snapshot.observed_at).toLocaleTimeString(language)
              : tr("Unknown")}
          </span>
        ),
      },
    ],
    [now, tr, language],
  );
  return (
    <>
    <DataList
      data={rows}
      columns={columns}
      label="workspaces"
      searchText={s => [s.root, s.identity.session_id, s.terminal || tr("Foreground session"), s.tty, s.pid, connectionState(s, now), tr(connectionState(s, now)), ...s.snapshot?.nodes.flatMap(n => [n.name, n.id, n.state, tr(n.state)]) ?? []].join(" ")}
      layout="workspaces"
      empty={tr("No foreground sessions")}
    />
    <p className="caption">{tr("Node totals include services and registered resources. Ready services have passed their readiness check; started processes and available resources keep their own states.")}</p>
    </>
  );
}
