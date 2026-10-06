export interface APIError {
  code: string;
  message: string;
}
export interface Identity {
  workspace_id: string;
  session_id: string;
  protocol_version: number;
  capabilities: string[];
}
export interface Metric {
  known: boolean;
  partial: boolean;
  sampled_at: string | null;
  rss_bytes: number | null;
  cpu_percent: number | null;
  uptime_millis: number | null;
}
export interface Node {
  id: string;
  project_id: string;
  name: string;
  kind: string;
  state: string;
  reason: string;
  ownership: string;
  allowed_actions: string[];
  resource_refs: string[];
  depends_on: string[];
  metric: Metric;
  metric_source?: string;
  metric_error?: string;
  ports: {
    port: number;
    status: string;
    reason: string;
    resource_refs: string[];
  }[];
}
export interface Snapshot {
  identity: Identity;
  revision: number;
  observed_at: string;
  nodes: Node[];
  processes: unknown[];
  containers: unknown[];
  diagnostics: { severity: string; code: string; message: string }[];
}
export interface Session {
  identity: Identity;
  root: string;
  pid: number;
  tty?: string;
  terminal?: string;
  started_at: string;
  available: boolean;
  stale: boolean;
  last_seen: string | null;
  snapshot?: Snapshot;
  error?: APIError;
}
export interface Resource {
  id: string;
  kind: string;
  identity_known: boolean;
  pid?: number;
  container_id?: string;
  endpoint_identity?: string;
  metric: Metric;
  references: {
    session_id: string;
    workspace_id: string;
    node_ids: string[];
    nodes: { id: string; state: string; ownership: string; role: string }[];
  }[];
}
export interface MetricTotal {
  count: number;
  known_memory_count: number;
  known_cpu_count: number;
  memory_bytes: number | null;
  cpu_percent: number | null;
  partial: boolean;
  sampled_at: string | null;
}
export interface Inventory {
  sessions: Session[];
  resources: Resource[];
  totals: { processes: MetricTotal; containers: MetricTotal };
  partial: boolean;
  collection_error?: APIError;
  collected_at: string;
  event_cursor: string;
}
export interface Plan {
  id: string;
  session_id: string;
  action: string;
  targets: string[];
  affected: string[];
  expires_at: string;
  fingerprint: string;
  warnings: string[];
}
export interface Operation {
  id: string;
  session_id: string;
  action: string;
  state: string;
  results: { target: string; state: string; error?: APIError }[];
  error?: APIError;
  created_at: string;
  updated_at: string;
}
export interface LogEntry {
  sequence: number;
  time: string;
  project_id: string;
  service_id: string;
  stream: string;
  text: string;
}
export interface LogPage {
  entries: LogEntry[];
  next_cursor: number;
  gap: boolean;
  dropped: number;
  reset: boolean;
  session_id: string;
  target: string;
  cursor: string;
}
export interface LogState {
  entries: LogEntry[];
  cursor: string;
  gap: number;
  unavailable?: string;
}
export const terminalStates = new Set([
  "succeeded",
  "failed",
  "partial",
  "canceled",
]);
export function sessionStale(s: Session, now = Date.now()) {
  return (
    s.stale ||
    !s.snapshot ||
    !Number.isFinite(Date.parse(s.snapshot.observed_at)) ||
    now - Date.parse(s.snapshot.observed_at) > 5000
  );
}
export function controllable(s: Session, now = Date.now()) {
  return (
    s.available && !sessionStale(s, now) && s.identity.protocol_version === 1
  );
}
