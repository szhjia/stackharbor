import { controllable, type Inventory, type Session, type Node } from "../api/types";
import { isAbnormalNodeState } from "./node-state";

// All counts describe this inventory snapshot; resources are already physically deduplicated by the API.
export function summarizeWorkbench(data: Inventory, now: number) {
  const fresh = data.sessions.filter(s => controllable(s, now)).length;
  const unavailable = data.sessions.filter(s => !s.available).length;
  const attention = data.sessions.filter(s => !controllable(s, now));
  const runtimeIssues: {session: Session; node?: Node; reason: string}[] = [];
  for (const session of data.sessions) {
    if (!controllable(session, now)) continue;
    for (const node of session.snapshot?.nodes ?? []) {
      // Observe-only nodes expect external listeners; port status describes ownership, not health.
      const conflicts = node.ports.filter(port => node.ownership !== "external" && port.status === "external");
      if (isAbnormalNodeState(node.state) || conflicts.length)
        runtimeIssues.push({session, node, reason: node.reason || conflicts.map(port => `:${port.port} · ${port.reason || port.status}`).join(", ") || node.state});
    }
    for (const diagnostic of session.snapshot?.diagnostics ?? [])
      if (diagnostic.severity === "error") runtimeIssues.push({session, reason: diagnostic.message});
  }
  const measured = data.resources.filter(r => r.metric.known && r.metric.rss_bytes != null && Number.isFinite(r.metric.rss_bytes) && r.metric.rss_bytes >= 0);
  return {
    fresh, unavailable, attention, runtimeIssues,
    needsAttention: attention.length - unavailable,
    workspaceCount: new Set(data.sessions.map(s => s.identity.workspace_id)).size,
    processes: data.resources.filter(r => r.kind === "process").length,
    containers: data.resources.filter(r => r.kind === "container").length,
    unknownMemory: data.resources.length - measured.length,
    topMemory: [...measured].sort((a, b) => b.metric.rss_bytes! - a.metric.rss_bytes! || a.id.localeCompare(b.id)).slice(0, 5),
  };
}
