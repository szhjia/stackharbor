import { controllable, type Inventory } from "../api/types";

// All counts describe this inventory snapshot; resources are already physically deduplicated by the API.
export function summarizeWorkbench(data: Inventory, now: number) {
  const fresh = data.sessions.filter(s => controllable(s, now)).length;
  const unavailable = data.sessions.filter(s => !s.available).length;
  const attention = data.sessions.filter(s => !controllable(s, now));
  const measured = data.resources.filter(r => r.metric.known && r.metric.rss_bytes != null && Number.isFinite(r.metric.rss_bytes) && r.metric.rss_bytes >= 0);
  return {
    fresh, unavailable, attention,
    needsAttention: attention.length - unavailable,
    workspaceCount: new Set(data.sessions.map(s => s.identity.workspace_id)).size,
    processes: data.resources.filter(r => r.kind === "process").length,
    containers: data.resources.filter(r => r.kind === "container").length,
    shared: data.resources.filter(r => new Set(r.references.flatMap(ref => ref.node_ids.map(id => `${ref.session_id}/${id}`))).size > 1).length,
    unknownIdentity: data.resources.filter(r => !r.identity_known).length,
    unknownMemory: data.resources.length - measured.length,
    topMemory: [...measured].sort((a, b) => b.metric.rss_bytes! - a.metric.rss_bytes! || a.id.localeCompare(b.id)).slice(0, 5),
  };
}
