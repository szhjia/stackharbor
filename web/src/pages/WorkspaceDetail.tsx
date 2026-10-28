import { usePreferences } from "../lib/preferences";
import { Link } from "react-router";
import { workspacePath } from "../routes";
import { createContext, useContext, useState, useMemo, useEffect } from "react";
import { matchesSearch, nodeSearchText, useListQuery } from "../lib/list-state";
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
import { summarizeWorkspaceNodes } from "../lib/workspace-summary";
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
const NodeActionContext = createContext({sid: "", usable: false, plan: (_action: string, _ids: string[], _port?: number) => {}});
function NodeActions({node}: {node: Node}) {
  const {t: tr} = usePreferences();
  const {sid, usable, plan} = useContext(NodeActionContext);
  const actions = node.allowed_actions.filter(action => action !== "release");
  return <div className="action-list node-actions">
    {node.allowed_actions.includes("release") ? node.ports.filter(port => port.status === "external").map(port =>
      <Button key={port.port} size="sm" variant="outline" disabled={!usable}
        aria-label={tr("Review port {port} for {name}", {port: port.port, name: node.id})}
        onClick={() => plan("release", [node.id], port.port)}>{tr("Resolve :{port}", {port: port.port})}</Button>) : null}
    <Button size="sm" variant="ghost" asChild><Link aria-label={tr("Logs for {name}", {name: node.id})} to={workspacePath(sid, "logs", node.id)}>{tr("Logs")}</Link></Button>
    {actions.length > 0 ? <DropdownMenu><DropdownMenuTrigger asChild>
      <Button size="icon-sm" variant="ghost" aria-label={tr("Actions for {name}", {name: node.id})} disabled={!usable} data-focus-key={`${sid}/${node.id}/actions`}><MoreHorizontal /></Button>
    </DropdownMenuTrigger><DropdownMenuContent align="end"><DropdownMenuGroup>
      {actions.map(action => <DropdownMenuItem key={action} data-focus-key={`${sid}/${node.id}/actions`} disabled={!usable} onSelect={() => plan(action, [node.id])}>{tr(action[0].toUpperCase() + action.slice(1))}</DropdownMenuItem>)}
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
  const {t: tr} = usePreferences();
  const cache = useQueryClient();
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [port, setPort] = useState("");
  const {search} = useListQuery();
  const sid = session.identity.session_id;
  const usable = controllable(session, now);
  const nodes = session.snapshot?.nodes ?? [];
  const summary = summarizeWorkspaceNodes(session.snapshot?.nodes);
  useEffect(() => { setSelected(new Set()); setPort(""); }, [sid, tab]);
  useEffect(() => { setPort(""); }, [selected]);
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
    () => ([
      {
        id: "select",
        header: tr("Select"),
        cell: ({ row }) => (
          <label className="node-selection" htmlFor={`select-${sid}-${row.original.id}`}><Checkbox
            id={`select-${sid}-${row.original.id}`}
            aria-label={tr("Select {name}", {name: row.original.id})}
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
          /></label>
        ),
      },
      {
        accessorKey: "id",
        header: tab === "tasks" ? tr("Task") : tr("Service"),
        cell: nodeIdentityCell,
      },
      {accessorKey: "state", header: tr("State"), cell: nodeStateCell},
      {id: "ports", header: tr("Ports"), cell: nodePortsCell},
      {id: "cpu", header: "CPU", cell: ({row}) => <MetricValue metric={row.original.metric} kind="cpu" />},
      {id: "memory", header: tr("Memory"), cell: ({row}) => <MetricValue metric={row.original.metric} kind="memory" />},
      {id: "actions", header: tr("Actions"), cell: nodeActionsCell},
    ] as ListColumn<Node>[]).filter(column => tab !== "tasks" || !["ports", "cpu", "memory"].includes(column.id ?? "")),
    [selected, usable, session, onPlan, tab, tr],
  );
  const scopedNodes = nodes.filter(n => tab === "tasks" ? n.kind === "task" : n.kind !== "task");
  const selectedNodes = scopedNodes.filter(n => selected.has(n.id));
  const hiddenSelected = selectedNodes.filter(n => !matchesSearch(nodeSearchText(n, tr), search)).length;
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
            <WorkspaceInfo session={session} />
            <Button size="sm" variant="outline" disabled={!usable} onClick={() => plan("close", [])}>{tr("Close session")}</Button>
          </div>
        </div>
        {tab === "services" ? (
          <DefinitionList layout="inline" label={tr("Workspace summary")} items={[
            {label: tr("Service / resource nodes"), value: summary?.services ?? tr("Unknown")},
            {label: tr("Ready services"), value: summary?.runningServices ?? tr("Unknown")},
            {label: tr("Tasks"), value: summary?.tasks ?? tr("Unknown")},
            {label: tr("Resources"), value: resources.filter(r => r.references.some(ref => ref.session_id === sid)).length},
          ]} />
        ) : null}
      </Panel>
      {!usable ? (
        <Notice
          title={
            session.identity.protocol_version !== 1
              ? tr("Unsupported protocol")
              : tr("State is unavailable or stale")
          }
        >{tr("Actions require a fresh snapshot from this exact session.")}{" "}</Notice>
      ) : null}
      {session.snapshot?.diagnostics.map((d, i) => (
        <Notice key={i} title={d.code} danger={d.severity === "error"}>
          {d.message}
        </Notice>
      ))}
      <Tabs value={tab} onValueChange={onTabChange}>
        <TabsList className="detail-tabs">
          <TabsTrigger value="services">{tr("Services")}</TabsTrigger>
          <TabsTrigger value="tasks">{tr("Tasks")}</TabsTrigger>
          <TabsTrigger value="resources">{tr("Resources")}</TabsTrigger>
          <TabsTrigger value="logs">{tr("Logs")}</TabsTrigger>
        </TabsList>
        {["services", "tasks"].map((t) => (
          <TabsContent value={t} key={t}>
            {selectedNodes.length > 0 ? <div className="toolbar node-toolbar">
              {selectedNodes.length > 0 ? <span>{tr("{count} nodes selected", {count: selectedNodes.length})}</span> : null}
              {hiddenSelected ? <span className="caption">{tr("{count} selected outside this filter", {count: hiddenSelected})}</span> : null}
              <div className="action-list">
                <Button variant="ghost" onClick={() => setSelected(new Set())}>{tr("Clear selection")}</Button>
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
                    {tr("{action} selected", {action: tr(a)})}{" "}</Button>
                ))}
              </div>
            </div> : null}
              <DataList
              data={nodes.filter((n) =>
                t === "tasks" ? n.kind === "task" : n.kind !== "task",
              )}
              columns={columns}
              searchText={node => nodeSearchText(node, tr)}
              label={t}
              layout={t === "tasks" ? "tasks" : "nodes"}
              empty={t === "tasks" ? tr("No tasks in this workspace") : tr("No services in this workspace")}
              emptyDescription={tr("Nodes declared by this workspace appear here.")}
              />
            {selectedNodes.length === 1 &&
            selectedNodes[0].allowed_actions.includes("release") ? (
              <Field>
                <FieldLabel htmlFor="port">{tr("Port to resolve")}</FieldLabel>
                <div className="action-list">
                  <Select value={port || "none"} onValueChange={value => setPort(value === "none" ? "" : value)}>
                    <SelectTrigger id="port" className="port-select"><SelectValue /></SelectTrigger>
                    <SelectContent position="popper"><SelectGroup>
                    <SelectItem value="none">{tr("Choose external declared port")}</SelectItem>
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
                      || !selectedNodes[0].ports.some(p => p.status === "external" && p.port === Number(port))
                    }
                    onClick={() =>
                      plan("release", [selectedNodes[0].id], Number(port))
                    }
                  >{tr("Review port resolution")}{" "}</Button>
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
            <FieldLabel htmlFor="log-target">{tr("Log target")}</FieldLabel>
            <Select value={target ? `node:${target}` : "all"} onValueChange={value => onTargetChange(value === "all" ? "" : value.slice(5))}>
              <SelectTrigger id="log-target" className="w-full"><SelectValue /></SelectTrigger>
              <SelectContent position="popper"><SelectGroup>
              <SelectItem value="all">{tr("All nodes")}</SelectItem>
              {target && !nodes.some((n) => n.id === target) ? <SelectItem value={`node:${target}`}>{target}{" "}{tr("(not currently observed)")}</SelectItem> : null}
              {nodes.map((n) => (
                <SelectItem key={n.id} value={`node:${n.id}`}>
                  {n.id}
                </SelectItem>
              ))}
              </SelectGroup></SelectContent>
            </Select>
          </Field>
          {logs.error ? (
            <Notice title={tr("Logs unavailable")}>
              {actionableError(logs.error)}
            </Notice>
          ) : null}
          <LogView
            key={`${sid}/${target}`}
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
