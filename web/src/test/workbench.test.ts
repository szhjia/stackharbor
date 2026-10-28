import { expect, it } from "vitest";
import type { Inventory, Node, Resource, Session } from "../api/types";
import { summarizeWorkbench } from "../lib/workbench";

const now = Date.parse("2026-10-09T10:00:00Z");
const total = {count: 0, known_memory_count: 0, known_cpu_count: 0, memory_bytes: null, cpu_percent: null, partial: false, sampled_at: null};
const inventory = (sessions: Session[], resources: Resource[]): Inventory => ({sessions, resources, totals: {processes: total, containers: total}, partial: false, collected_at: new Date(now).toISOString(), event_cursor: "1"});
function session(id: string, age = 0): Session {
  const identity = {session_id: id, workspace_id: "same-workspace", protocol_version: 1, capabilities: []};
  return {identity, root: "/workspace", pid: 1, started_at: "", last_seen: null, available: true, stale: false, snapshot: {identity, revision: 1, observed_at: new Date(now - age).toISOString(), nodes: [], processes: [], containers: [], diagnostics: []}};
}
function resource(id: string, value: number | null, known = true): Resource {
  return {id, kind: "process", identity_known: true, references: [], metric: {known, partial: false, rss_bytes: value, cpu_percent: null, sampled_at: null, uptime_millis: null}};
}
function externalListenerNode(patch: Partial<Node> = {}): Node {
  return {id: "app/observer", name: "Observer", project_id: "app", kind: "service", state: "running", reason: "External service · read-only dependency", ownership: "external", allowed_actions: [], depends_on: [], resource_refs: [], metric: resource("r", null).metric, ports: [{port: 5432, status: "external", reason: "", resource_refs: []}], ...patch};
}
it.each(["running", "available"])("does not report a healthy external observe node in state %s as a port conflict", state => {
  const fresh = session("fresh");
  fresh.snapshot!.nodes = [externalListenerNode({state, kind: state === "available" ? "resource" : "service"})];
  expect(summarizeWorkbench(inventory([fresh], []), now).runtimeIssues).toEqual([]);
});
it.each(["running", "stopped", "waiting"])("preserves external port conflicts on session-owned nodes in state %s", state => {
  const fresh = session("fresh");
  fresh.snapshot!.nodes = [externalListenerNode({id: "app/api", ownership: "session", state, reason: ""})];
  const issues = summarizeWorkbench(inventory([fresh], []), now).runtimeIssues;
  expect(issues).toHaveLength(1);
  expect(issues[0].node?.id).toBe("app/api");
  expect(issues[0].reason).toBe(":5432 · external");
});
it.each(["failed", "error", "blocked"])("preserves %s failures on observe nodes with external listeners", state => {
  const fresh = session("fresh");
  fresh.snapshot!.nodes = [externalListenerNode({state, reason: "External process command does not match"})];
  const issues = summarizeWorkbench(inventory([fresh], []), now).runtimeIssues;
  expect(issues).toHaveLength(1);
  expect(issues[0].reason).toBe("External process command does not match");
});
it.each(["unready", "unavailable", "unhealthy", "unknown"])("reports %s health and identity problems even without a port conflict", state => {
  const fresh = session("fresh");
  fresh.snapshot!.nodes = [externalListenerNode({state, kind: state === "unready" ? "service" : "resource", ownership: "session", ports: [{port: 5432, status: "owned", reason: "", resource_refs: []}], reason: "Health or identity verification failed"})];
  const summary = summarizeWorkbench(inventory([fresh], []), now);
  expect(summary.runtimeIssues).toEqual([{session: fresh, node: fresh.snapshot!.nodes[0], reason: "Health or identity verification failed"}]);
  expect(summarizeWorkbench(inventory([fresh], []), now + 6000).runtimeIssues).toEqual([]);
});
it.each(["running", "available", "started", "succeeded", "stopped", "waiting", "starting", "stopping", "checking", "running-task", "verifying"])("does not report normal or transitional state %s as an incident", state => {
  const fresh = session("fresh");
  fresh.snapshot!.nodes = [externalListenerNode({state, ports: []})];
  expect(summarizeWorkbench(inventory([fresh], []), now).runtimeIssues).toEqual([]);
});
it("keeps stale, unsupported and unavailable sessions disjoint as time advances", () => {
  const sessions = [session("fresh"), session("old", 6000), {...session("offline"), available: false}, {...session("new-protocol"), identity: {...session("new-protocol").identity, protocol_version: 2}}];
  const data = inventory(sessions, []);
  const summary = summarizeWorkbench(data, now);
  expect(summary.workspaceCount).toBe(1);
  expect([summary.fresh, summary.needsAttention, summary.unavailable]).toEqual([1, 2, 1]);
  expect(summarizeWorkbench(data, now + 6000).fresh).toBe(0);
});
it("ranks measured physical resources once, keeps known zero, and excludes unknown or invalid memory", () => {
  const shared = resource("shared", 100);
  const ref = {session_id: "s", workspace_id: "w", node_ids: ["api"], nodes: []};
  shared.references = [ref, ref, {...ref, node_ids: ["web"]}];
  const data = inventory([], [shared, resource("zero", 0), resource("missing", null), resource("unknown", 9999, false), resource("nan", NaN), resource("negative", -1)]);
  const summary = summarizeWorkbench(data, now);
  expect(summary.topMemory.map(r => r.id)).toEqual(["shared", "zero"]);
  expect(summary.unknownMemory).toBe(4);
  expect(data.resources[0]).toBe(shared);
});
it("limits ranking to five without changing resource order", () => {
  const data = inventory([], Array.from({length: 8}, (_, i) => resource(String(i), i)));
  expect(summarizeWorkbench(data, now).topMemory.map(r => r.id)).toEqual(["7", "6", "5", "4", "3"]);
  expect(data.resources[0].id).toBe("0");
});
it("surfaces current node failures and port conflicts without treating stopped nodes or stale snapshots as fresh incidents", () => {
  const fresh = session("fresh");
  const node = {id: "api", name: "API", project_id: "app", kind: "service", state: "failed", reason: "Port occupied", ownership: "session", allowed_actions: [], depends_on: [], resource_refs: [], metric: resource("r", 0).metric, ports: []};
  fresh.snapshot!.nodes = [node, {...node, id: "stopped", state: "stopped", reason: ""}, {...node, id: "conflict", state: "blocked", ports: [{port: 5173, status: "external", reason: "Occupied", resource_refs: []}]}];
  const stale = {...fresh, identity: {...fresh.identity, session_id: "stale"}, stale: true};
  const summary = summarizeWorkbench(inventory([fresh, stale], []), now);
  expect(summary.runtimeIssues.map(issue => issue.node?.id)).toEqual(["api", "conflict"]);
  expect(summary.runtimeIssues[0].session.identity.session_id).toBe("fresh");
  expect(summary.attention.map(s => s.identity.session_id)).toEqual(["stale"]);
});
