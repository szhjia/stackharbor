import { Link } from "react-router";
import { workspacePath } from "../routes";
import { createContext, useContext, useState, useMemo } from "react";
import { MoreHorizontal } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import type { Session, Node, Resource, LogState } from "../api/types";
import { controllable } from "../api/types";
import { api, actionableError } from "../api/client";
import { logKey, mergeLogPage } from "../api/logs";
import { DataList } from "../components/DataList";
import { MetricValue } from "../components/MetricView";
import { Panel } from "../components/Panel";
import { DefinitionList } from "../components/DefinitionList";
import { NodeInfo } from "../components/NodeInfo";
import { WorkspaceInfo } from "../components/WorkspaceInfo";
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem } from "../components/ui/dropdown-menu";
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
import { Field, FieldLabel } from "../components/ui/field";
import { Select, SelectTrigger, SelectValue, SelectContent, SelectGroup, SelectItem } from "../components/ui/select";
import type { ListColumn } from "../components/DataList";
export interface ActionRequest {
  session: Session;
  action: string;
  targets: string[];
  port?: number;
}
const NodeActionContext = createContext({sid: "", usable: false, plan: (_action: string, _ids: string[]) => {}});
function NodeActions({node}: {node: Node}) {
  const {sid, usable, plan} = useContext(NodeActionContext);
  const actions = node.allowed_actions.filter(action => action !== "release");
  return <div className="action-list node-actions">
    <Button size="sm" variant="ghost" asChild><Link aria-label={`Logs for ${node.id}`} to={workspacePath(sid, "logs", node.id)}>Logs</Link></Button>
    {actions.length > 0 ? <DropdownMenu><DropdownMenuTrigger asChild>
      <Button size="icon-sm" variant="ghost" aria-label={`Actions for ${node.id}`} disabled={!usable} data-focus-key={`${sid}/${node.id}/actions`}><MoreHorizontal /></Button>
    </DropdownMenuTrigger><DropdownMenuContent align="end"><DropdownMenuGroup>
      {actions.map(action => <DropdownMenuItem key={action} data-focus-key={`${sid}/${node.id}/actions`} disabled={!usable} onSelect={() => plan(action, [node.id])}>{action[0].toUpperCase() + action.slice(1)}</DropdownMenuItem>)}
    </DropdownMenuGroup></DropdownMenuContent></DropdownMenu> : null}
  </div>;
}
const nodeIdentityCell: ListColumn<Node>["cell"] = ({row}) => <NodeInfo node={row.original} kind="identity" />;
const nodeStateCell: ListColumn<Node>["cell"] = ({row}) => <NodeInfo node={row.original} kind="state" />;
const nodePortsCell: ListColumn<Node>["cell"] = ({row}) => <NodeInfo node={row.original} kind="ports" />;
const nodeActionsCell: ListColumn<Node>["cell"] = ({row}) => <NodeActions node={row.original} />;
export function WorkspaceDetail({
  session,
  resources,
  sessions,
  now,
  connected,
  onPlan,
  tab,
  target,
  onTabChange,
  onTargetChange,
}: {
  session: Session;
  resources: Resource[];
  sessions: Session[];
  now: number;
  connected: boolean;
  onPlan: (requests: ActionRequest[]) => void;
  tab: string;
  target: string;
  onTabChange: (tab: string) => void;
  onTargetChange: (target: string) => void;
}) {
  const cache = useQueryClient();
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [port, setPort] = useState("");
  const sid = session.identity.session_id;
  const usable = controllable(session, now);
  const nodes = session.snapshot?.nodes ?? [];
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
        header: tab === "tasks" ? "Task" : "Service",
        cell: nodeIdentityCell,
      },
      {accessorKey: "state", header: "State", cell: nodeStateCell},
      {id: "ports", header: "Ports", cell: nodePortsCell},
      {id: "cpu", header: "CPU", cell: ({row}) => <MetricValue metric={row.original.metric} kind="cpu" />},
      {id: "memory", header: "Memory", cell: ({row}) => <MetricValue metric={row.original.metric} kind="memory" />},
      {id: "actions", header: "Actions", cell: nodeActionsCell},
    ],
    [selected, usable, session, onPlan, tab],
  );
  const selectedNodes = nodes.filter((n) => selected.has(n.id));
  const bulkActions = ["start", "stop", "restart"].filter(
    (a) =>
      selectedNodes.length > 0 &&
      selectedNodes.every((n) => n.allowed_actions.includes(a)),
  );
  return (
    <NodeActionContext.Provider value={{sid, usable, plan}}>
    <section className="detail workspace-detail">
      <Panel className="workspace-summary-card">
        <div className="workspace-summary-top">
          <p className="workspace-path mono">{session.root}</p>
          <div className="workspace-summary-actions">
            <WorkspaceInfo session={session} kind="session" />
            <Button size="sm" variant="outline" disabled={!usable} onClick={() => plan("close", [])}>Close session</Button>
          </div>
        </div>
        <DefinitionList layout="inline" label="Workspace summary" items={[
          {label: "Services", value: nodes.filter(n => n.kind !== "task").length},
          {label: "Running", value: nodes.filter(n => n.kind !== "task" && n.state === "running").length},
          {label: "Tasks", value: nodes.filter(n => n.kind === "task").length},
          {label: "Resources", value: resources.filter(r => r.references.some(ref => ref.session_id === sid)).length},
        ]} />
      </Panel>
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
      <Tabs value={tab} onValueChange={onTabChange}>
        <TabsList className="detail-tabs">
          <TabsTrigger value="services">Services</TabsTrigger>
          <TabsTrigger value="tasks">Tasks</TabsTrigger>
          <TabsTrigger value="resources">Resources</TabsTrigger>
          <TabsTrigger value="logs">Logs</TabsTrigger>
        </TabsList>
        {["services", "tasks"].map((t) => (
          <TabsContent value={t} key={t}>
            {selectedNodes.length > 0 ? <div className="toolbar node-toolbar">
              {selectedNodes.length > 0 ? <span>{selectedNodes.length} selected</span> : null}
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
            </div> : null}
              <DataList
              data={nodes.filter((n) =>
                t === "tasks" ? n.kind === "task" : n.kind !== "task",
              )}
              columns={columns}
              label={t}
              layout="nodes"
              empty={t === "tasks" ? "No tasks in this workspace" : "No services in this workspace"}
              emptyDescription="Nodes declared by this workspace appear here."
              />
            {selectedNodes.length === 1 &&
            selectedNodes[0].allowed_actions.includes("release") ? (
              <Field>
                <FieldLabel htmlFor="port">Port to resolve</FieldLabel>
                <div className="action-list">
                  <Select value={port || "none"} onValueChange={value => setPort(value === "none" ? "" : value)}>
                    <SelectTrigger id="port" className="port-select"><SelectValue /></SelectTrigger>
                    <SelectContent position="popper"><SelectGroup>
                    <SelectItem value="none">Choose external declared port</SelectItem>
                    {selectedNodes[0].ports
                      .filter((p) => p.status === "external")
                      .map((p) => (
                        <SelectItem key={p.port} value={String(p.port)}>
                          {p.port} · {p.reason || p.status}
                        </SelectItem>
                      ))}
                    </SelectGroup></SelectContent>
                  </Select>
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
          />
        </TabsContent>
        <TabsContent value="logs">
          <Field className="filter-field">
            <FieldLabel htmlFor="log-target">Log target</FieldLabel>
            <Select value={target ? `node:${target}` : "all"} onValueChange={value => onTargetChange(value === "all" ? "" : value.slice(5))}>
              <SelectTrigger id="log-target" className="w-full"><SelectValue /></SelectTrigger>
              <SelectContent position="popper"><SelectGroup>
              <SelectItem value="all">All nodes</SelectItem>
              {target && !nodes.some((n) => n.id === target) ? <SelectItem value={`node:${target}`}>{target} (not currently observed)</SelectItem> : null}
              {nodes.map((n) => (
                <SelectItem key={n.id} value={`node:${n.id}`}>
                  {n.id}
                </SelectItem>
              ))}
              </SelectGroup></SelectContent>
            </Select>
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
    </NodeActionContext.Provider>
  );
}
