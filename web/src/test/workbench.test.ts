import { expect, it } from "vitest";
import type { Inventory, Resource, Session } from "../api/types";
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
  expect(summary.shared).toBe(1);
  expect(summary.unknownMemory).toBe(4);
  expect(data.resources[0]).toBe(shared);
});
it("limits ranking to five without changing resource order", () => {
  const data = inventory([], Array.from({length: 8}, (_, i) => resource(String(i), i)));
  expect(summarizeWorkbench(data, now).topMemory.map(r => r.id)).toEqual(["7", "6", "5", "4", "3"]);
  expect(data.resources[0].id).toBe("0");
});
