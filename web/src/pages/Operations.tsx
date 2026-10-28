import { usePreferences } from "../lib/preferences";
import { Link, useSearchParams } from "react-router";
import { workspacePath } from "../routes";
import { useQueries, useQueryClient, type Query } from "@tanstack/react-query";
import { api, ApiFailure, actionableError } from "../api/client";
import type { Session, Operation } from "../api/types";
import { terminalStates } from "../api/types";
import { DataList } from "../components/DataList";
import { Status } from "../components/Status";
import { Button } from "../components/ui/button";
import { Notice } from "../components/Notice";
import type { ListColumn } from "../components/DataList";
export interface Tracked {
  session_id: string;
  id: string;
}
export function Operations({
  sessions,
  tracked,
  onError,
}: {
  sessions: Session[];
  tracked: Tracked[];
  onError: (s: string) => void;
}) {
  const {t: tr, language} = usePreferences();
  const cache = useQueryClient();
  const roots = new Map(sessions.map(s => [s.identity.session_id, s.root]));
  const [params] = useSearchParams();
  const sessionID = params.get("session");
  const operationID = params.get("operation");
  const requested = new Map(tracked.map(operation => [`${operation.session_id}/${operation.id}`, operation]));
  if (sessionID && operationID) requested.set(`${sessionID}/${operationID}`, {session_id: sessionID, id: operationID});
  const lists = useQueries({
    queries: sessions.map((s) => ({
      queryKey: ["operations", s.identity.session_id],
      queryFn: () => api.operations(s.identity.session_id),
      retry: false,
      refetchInterval: 1000,
    })),
  });
  const exact = useQueries({
    queries: [...requested.values()].map((o) => ({
      queryKey: ["operation", o.session_id, o.id],
      queryFn: () => api.operation(o.session_id, o.id),
      retry: false,
      refetchInterval: (q: Query<Operation>) => {
        if (terminalStates.has(q.state.data?.state ?? "")) return false;
        if (!q.state.error) return 1000;
        // A missing exact session cannot answer repeated lookups. Keep the
        // unknown outcome available for explicit retry or connection recovery.
        if (!sessions.some(s => s.identity.session_id === o.session_id && s.available)
          || (q.state.error instanceof ApiFailure && [404, 410].includes(q.state.error.status))) return false;
        return Math.min(30000, 1000 * 2 ** Math.min(q.state.errorUpdateCount, 5));
      },
    })),
  });
  const rows = new Map<string, Operation>();
  for (const query of [...lists, ...exact]) {
    if (query.data) {
      for (const op of Array.isArray(query.data) ? query.data : [query.data])
        rows.set(op.session_id + "/" + op.id, op);
    }
  }
  const columns: ListColumn<Operation>[] = [
    {
      accessorKey: "id",
      header: tr("Operation"),
      cell: ({ row }) => (
        <div className="record">
          <strong>{tr(row.original.action)}</strong>
          <Link to={workspacePath(row.original.session_id)}>{roots.get(row.original.session_id) ?? row.original.session_id}</Link>
          <Link className="mono caption" to={`/operations?${new URLSearchParams({session: row.original.session_id, operation: row.original.id, q: row.original.id})}`}>{row.original.id}</Link>
          <span className="mono caption">{tr("Session")}{" "}{row.original.session_id}
          </span>
          <span className="caption">{tr("Created")} · <time dateTime={row.original.created_at}>{new Date(row.original.created_at).toLocaleString(language)}</time></span>
          {row.original.updated_at ? <span className="caption">{tr("Updated")} · <time dateTime={row.original.updated_at}>{new Date(row.original.updated_at).toLocaleString(language)}</time></span> : null}
        </div>
      ),
    },
    {
      accessorKey: "state",
      header: tr("Result"),
      cell: ({ row }) => <Status value={row.original.state} />,
    },
    {
      id: "results",
      header: tr("Per-target outcomes"),
      cell: ({ row }) => (
        <div className="record">
          {row.original.results.map((r) => (
            <span key={r.target}>
              <Link className="mono" aria-label={tr("Logs for {name}", {name: r.target})} to={workspacePath(row.original.session_id, "logs", r.target)}>{r.target}</Link> · {tr(r.state)}
              {r.error ? ` · ${r.error.message}` : ""}
            </span>
          ))}
          {row.original.error ? (
            <span>{row.original.error.message}</span>
          ) : null}
        </div>
      ),
    },
    {
      id: "cancel",
      header: tr("Control"),
      cell: ({ row }) =>
        !terminalStates.has(row.original.state) ? (
          <Button
            variant="outline"
            onClick={() =>
              void api
                .cancel(row.original.session_id, row.original.id)
                .catch((e) => onError(actionableError(e)))
            }
          >{tr("Cancel operation")}{" "}</Button>
        ) : null,
    },
  ];
  return (
    <>
      {[...lists, ...exact].some((q) => q.error) ? (
        <Notice title={tr("Some operation outcomes are unavailable")}>
          <p>{tr("Reconnect to the exact session. Missing results do not imply success.")}</p>
          <Button variant="outline" onClick={() => void cache.invalidateQueries({
            predicate: query => ["operation", "operations"].includes(String(query.queryKey[0])) && query.state.status === "error",
          })}>{tr("Retry unavailable outcomes")}</Button>
        </Notice>
      ) : null}
      <DataList
        data={[...rows.values()].sort((a, b) =>
          b.created_at.localeCompare(a.created_at),
        )}
        columns={columns}
        searchText={op => [op.id, op.session_id, roots.get(op.session_id), op.action, tr(op.action), op.state, tr(op.state), op.error?.message, ...op.results.flatMap(r => [r.target, r.state, tr(r.state), r.error?.message])].join(" ")}
        label="operations"
        empty={tr("No recorded operations")}
        emptyDescription={tr("Reviewed actions appear here with their per-target results.")}
      />
    </>
  );
}
