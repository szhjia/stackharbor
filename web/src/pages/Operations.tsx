import { usePreferences } from "../lib/preferences";
import { useQueries } from "@tanstack/react-query";
import { api, actionableError } from "../api/client";
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
  const {t: tr} = usePreferences();
  const lists = useQueries({
    queries: sessions.map((s) => ({
      queryKey: ["operations", s.identity.session_id],
      queryFn: () => api.operations(s.identity.session_id),
      retry: false,
      refetchInterval: 1000,
    })),
  });
  const exact = useQueries({
    queries: tracked.map((o) => ({
      queryKey: ["operation", o.session_id, o.id],
      queryFn: () => api.operation(o.session_id, o.id),
      retry: false,
      refetchInterval: (q: any) =>
        terminalStates.has(q.state.data?.state) ? false : 1000,
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
          <span className="mono caption">{row.original.id}</span>
          <span className="mono caption">{tr("Session")}{" "}{row.original.session_id}
          </span>
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
              <span className="mono">{r.target}</span> · {tr(r.state)}
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
        <Notice title={tr("Some operation outcomes are unavailable")}>{tr("Reconnect to the exact session. Missing results do not imply success.")}{" "}</Notice>
      ) : null}
      <DataList
        data={[...rows.values()].sort((a, b) =>
          b.created_at.localeCompare(a.created_at),
        )}
        columns={columns}
        label="operations"
        empty={tr("No recorded operations")}
      />
    </>
  );
}
