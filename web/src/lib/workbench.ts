import { controllable, type Inventory, type Session, type Node, type Resource } from "../api/types";
import { isAbnormalNodeState } from "./node-state";

export function presentWorkbenchResource(resource: Resource, sessions: Session[]) {
  const sessionByID = new Map(sessions.map(session => [session.identity.session_id, session]));
  const references = resource.references.map(reference => {
    const session = sessionByID.get(reference.session_id);
    const workspace = session?.root.split("/").filter(Boolean).at(-1) || reference.workspace_id;
    const associations = reference.node_ids.map(id => ({workspace, name: session?.snapshot?.nodes.find(node => node.id === id)?.name || id}));
    return {workspace, associations};
  });
  const associations = references.flatMap(reference => reference.associations);
  const workspace = [...new Set(references.map(reference => reference.workspace).filter(Boolean))].join(", ");
  const identity = resource.kind === "process" && resource.pid != null
    ? `PID ${resource.pid}`
    : resource.container_id?.slice(0, 12) || "";
  return {
    title: associations[0]?.name || identity,
    additionalAssociations: Math.max(0, associations.length - 1),
    workspace,
    identity,
    fullTitle: associations.length ? associations.map(association => `${association.workspace} / ${association.name}`).join(", ") : identity,
  };
}

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
