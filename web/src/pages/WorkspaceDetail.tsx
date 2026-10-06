import { useState, useMemo, useEffect } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import type { Session, Node, Resource, LogState } from "../api/types";
import { controllable } from "../api/types";
import { api, actionableError } from "../api/client";
import { logKey, mergeLogPage } from "../api/logs";
import { DataList } from "../components/DataList";
import { MetricView } from "../components/MetricView";
import { Status } from "../components/Status";
import { Notice } from "../components/Notice";
import { LogView } from "../components/LogView";
import { Resources } from "./Resources";
import {
  Tabs,
  TabsList,
  TabsTrigger,
  TabsContent,
} from "../components/ui/tabs";
import { Button } from "../components/ui/button";
import { Checkbox } from "../components/ui/checkbox";
import { Input } from "../components/ui/input";
import { Field, FieldLabel } from "../components/ui/field";
import type { ListColumn } from "../components/DataList";
export interface ActionRequest {
  session: Session;
  action: string;
  targets: string[];
  port?: number;
}
export function WorkspaceDetail({
  session,
  resources,
  sessions,
  now,
  connected,
  onPlan,
  onLogFilter,
  onOpen,
}: {
  session: Session;
  resources: Resource[];
  sessions: Session[];
  now: number;
  connected: boolean;
  onPlan: (requests: ActionRequest[]) => void;
  onLogFilter: (target: string | undefined) => void;
  onOpen: (id: string) => void;
}) {
  const cache = useQueryClient();
  const [tab, setTab] = useState("services");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [target, setTarget] = useState("");
  const [port, setPort] = useState("");
  const sid = session.identity.session_id;
  const usable = controllable(session, now);
  const nodes = session.snapshot?.nodes ?? [];
  useEffect(() => {
    onLogFilter(tab === "logs" ? target : undefined);
    return () => onLogFilter(undefined);
  }, [tab, target, sid]);
  const logs = useQuery({
    queryKey: logKey(sid, target),
    queryFn: async (): Promise<LogState> => {
      const before = cache.getQueryData<LogState>(logKey(sid, target));
      const p = await api.logs(sid, target, before?.cursor ?? "");
      const old = cache.getQueryData<LogState>(logKey(sid, target));
      const entries = mergeLogPage(old?.entries ?? [], p);
      return {
        entries,
        cursor:
          !p.reset &&
          old &&
          Math.max(0, ...old.entries.map((e) => e.sequence)) > p.next_cursor
            ? old.cursor
            : p.cursor,
        gap: p.gap ? p.dropped : (old?.gap ?? 0),
      };
    },
    enabled: tab === "logs",
    retry: false,
    staleTime: Infinity,
  });
  const plan = (action: string, ids: string[], newPort?: number) =>
    onPlan([
      { session, action, targets: ids, ...(newPort ? { port: newPort } : {}) },
    ]);
  const columns = useMemo<ListColumn<Node>[]>(
    () => [
      {
        id: "select",
        header: "Select",
        cell: ({ row }) => (
          <Checkbox
            aria-label={`Select ${row.original.id}`}
            checked={selected.has(row.original.id)}
            disabled={!usable || row.original.allowed_actions.length === 0}
            onCheckedChange={(v) =>
              setSelected((old) => {
                const next = new Set(old);
                if (v) next.add(row.original.id);
                else next.delete(row.original.id);
                return next;
              })
            }
          />
        ),
      },
      {
        accessorKey: "id",
        header: "Node / ownership",
        cell: ({ row }) => (
          <div className="record">
            <strong>{row.original.name || row.original.id}</strong>
            <span className="mono caption">{row.original.id}</span>
            <Status value={row.original.ownership} />
            {row.original.depends_on?.length ? (
              <span className="caption">
                Requires {row.original.depends_on.join(", ")}
              </span>
            ) : null}
            {row.original.resource_refs.length ? (
              <span className="caption mono">
                {row.original.resource_refs.join(", ")}
              </span>
            ) : null}
          </div>
        ),
      },
      {
        accessorKey: "state",
        header: "State / ports",
        cell: ({ row }) => (
          <div className="record">
            <Status value={row.original.state} />
            {row.original.reason ? (
              <span className="caption">{row.original.reason}</span>
            ) : null}
            {row.original.ports.map((p) => (
              <span key={p.port} className="mono caption">
                :{p.port} · {p.status}
                {p.reason ? ` · ${p.reason}` : ""}
              </span>
            ))}
          </div>
        ),
      },
      {
        id: "metrics",
        header: "CPU / RSS",
        cell: ({ row }) => <MetricView metric={row.original.metric} />,
      },
      {
        id: "actions",
        header: "Actions",
        cell: ({ row }) => (
          <div className="action-list">
            {row.original.allowed_actions
              .filter((action) => action !== "release")
              .map((action) => (
                <Button
                  data-focus-key={sid + "/" + row.original.id + "/" + action}
                  key={action}
                  size="sm"
                  variant="outline"
                  disabled={!usable}
                  onClick={() => plan(action, [row.original.id])}
                >
                  {action}
                </Button>
              ))}
            <Button
              size="sm"
              variant="ghost"
              onClick={() => {
                setTarget(row.original.id);
                setTab("logs");
              }}
            >
              Logs
            </Button>
          </div>
        ),
      },
    ],
    [selected, usable, session, onPlan],
  );
  const selectedNodes = nodes.filter((n) => selected.has(n.id));
  const bulkActions = ["start", "stop", "restart"].filter(
    (a) =>
      selectedNodes.length > 0 &&
      selectedNodes.every((n) => n.allowed_actions.includes(a)),
  );
  return (
    <section className="detail">
      <div className="detail-title">
        <div>
          <h2>{session.root.split("/").filter(Boolean).at(-1)}</h2>
          <p className="mono caption">{session.root}</p>
          <p className="mono caption">Session {sid}</p>
        </div>
        <Button
          variant="outline"
          disabled={!usable}
          onClick={() => plan("close", [])}
        >
          Close session
        </Button>
      </div>
      {!usable ? (
        <Notice
          title={
            session.identity.protocol_version !== 1
              ? "Unsupported protocol"
              : "State is unavailable or stale"
          }
        >
          Actions require a fresh snapshot from this exact session.
        </Notice>
      ) : null}
      {session.snapshot?.diagnostics.map((d, i) => (
        <Notice key={i} title={d.code} danger={d.severity === "error"}>
          {d.message}
        </Notice>
      ))}
      <Tabs value={tab} onValueChange={setTab}>
        <TabsList className="detail-tabs">
          <TabsTrigger value="services">Services</TabsTrigger>
          <TabsTrigger value="tasks">Tasks</TabsTrigger>
          <TabsTrigger value="resources">Resources</TabsTrigger>
          <TabsTrigger value="logs">Logs</TabsTrigger>
        </TabsList>
        {["services", "tasks"].map((t) => (
          <TabsContent value={t} key={t}>
            <div className="toolbar">
              <span>{selectedNodes.length} selected</span>
              <div className="action-list">
                {bulkActions.map((a) => (
                  <Button
                    variant="outline"
                    key={a}
                    disabled={!usable}
                    onClick={() =>
                      plan(
                        a,
                        selectedNodes.map((n) => n.id),
                      )
                    }
                  >
                    {a} selected
                  </Button>
                ))}
              </div>
            </div>
            <DataList
              data={nodes.filter((n) =>
                t === "tasks" ? n.kind === "task" : n.kind !== "task",
              )}
              columns={columns}
              label={t}
            />
            {selectedNodes.length === 1 &&
            selectedNodes[0].allowed_actions.includes("release") ? (
              <Field>
                <FieldLabel htmlFor="port">Port to resolve</FieldLabel>
                <div className="action-list">
                  <select
                    id="port"
                    value={port}
                    onChange={(e) => setPort(e.target.value)}
                  >
                    <option value="">Choose external declared port</option>
                    {selectedNodes[0].ports
                      .filter((p) => p.status === "external")
                      .map((p) => (
                        <option key={p.port} value={p.port}>
                          {p.port} · {p.reason || p.status}
                        </option>
                      ))}
                  </select>
                  <Button
                    disabled={
                      !usable ||
                      !/^\d+$/.test(port) ||
                      Number(port) < 1 ||
                      Number(port) > 65535
                    }
                    onClick={() =>
                      plan("release", [selectedNodes[0].id], Number(port))
                    }
                  >
                    Review port resolution
                  </Button>
                </div>
              </Field>
            ) : null}
          </TabsContent>
        ))}
        <TabsContent value="resources">
          <Resources
            resources={resources.filter((r) =>
              r.references.some((ref) => ref.session_id === sid),
            )}
            sessions={sessions}
            onOpen={onOpen}
          />
        </TabsContent>
        <TabsContent value="logs">
          <Field className="filter-field">
            <FieldLabel htmlFor="log-target">Log target</FieldLabel>
            <select
              id="log-target"
              value={target}
              onChange={(e) => setTarget(e.target.value)}
            >
              <option value="">All nodes</option>
              {nodes.map((n) => (
                <option key={n.id} value={n.id}>
                  {n.id}
                </option>
              ))}
            </select>
          </Field>
          {logs.error ? (
            <Notice title="Logs unavailable">
              {actionableError(logs.error)}
            </Notice>
          ) : null}
          <LogView
            entries={logs.data?.entries ?? []}
            gap={logs.data?.gap ?? 0}
            connected={connected}
            unavailable={logs.data?.unavailable}
          />
        </TabsContent>
      </Tabs>
    </section>
  );
}
