import { Link } from "react-router";
import { useConsole } from "../layout/AppShell";
import { controllable } from "../api/types";
import { Workspaces } from "./Workspaces";
import { Resources } from "./Resources";
import { Operations } from "./Operations";
import { WorkspaceDetail } from "./WorkspaceDetail";
import { SummaryCard } from "../components/SummaryCard";
import { Notice } from "../components/Notice";
import { Button } from "../components/ui/button";
import { memory, cpu } from "../components/MetricView";

export function OverviewPage() {
  const {data, sessions, now} = useConsole();
  const fresh = sessions.filter((s) => controllable(s, now)).length;
  const unavailable = sessions.filter((s) => !s.available).length;
  const workspaceCount = new Set(sessions.map((s) => s.identity.workspace_id)).size;
  const resources = data.resources;
  const shared = resources.filter((resource) => new Set(resource.references.flatMap((ref) =>
    ref.node_ids.map((id) => `${ref.session_id}/${id}`),
  )).size > 1).length;
  return <section className="route-page">
    <section className="observation-summary">
      <div>
        <span className="caption">SESSION COVERAGE</span>
        <strong>
          {fresh}
          <small> / {sessions.length} fresh</small>
        </strong>
        <p>
          {data.partial ? "Partial inventory" : "Current inventory"}
        </p>
      </div>
      {(["processes", "containers"] as const).map((kind) => (
        <div key={kind}>
          <span className="caption">
            {kind === "processes"
              ? "PROCESS RSS"
              : "CONTAINER WORKING SET"}
          </span>
          <strong>{memory(data.totals[kind].memory_bytes)}</strong>
          <p>
            {data.totals[kind].count} {kind} · CPU{" "}
            {cpu(data.totals[kind].cpu_percent)}
            {data.totals[kind].partial ? " · partial" : ""}
          </p>
        </div>
      ))}
    </section>
    <div className="overview-summaries">
      <SummaryCard id="workspace-summary-title" title="Workspaces" href="/workspaces" total={workspaceCount}
        unit={`${workspaceCount === 1 ? "workspace" : "workspaces"} · ${sessions.length} ${sessions.length === 1 ? "session" : "sessions"}`}
        items={[
          {label: "Fresh sessions", value: fresh},
          {label: "Needs attention", value: sessions.length - fresh - unavailable},
          {label: "Unavailable", value: unavailable},
        ]}
        description={sessions.length === 0 ? "No foreground sessions" : "Needs attention includes stale snapshots and unsupported protocols."} />
      <SummaryCard id="resource-summary-title" title="Resources" href="/resources" total={resources.length} unit="physical resources"
        items={[
          {label: "Processes", value: resources.filter(r => r.kind === "process").length},
          {label: "Containers", value: resources.filter(r => r.kind === "container").length},
          {label: "Shared", value: shared},
          {label: "Unknown identity", value: resources.filter(r => !r.identity_known).length},
        ]}
        description={resources.length === 0 ? "No observed resources" : "Each physical resource is counted once. Shared resources serve multiple nodes."} />
    </div>
    <p className="caption">Observed {new Date(data.collected_at).toLocaleTimeString()}{data.partial ? " · Partial inventory; retained observations may be stale." : ""}</p>
  </section>;
}
export function WorkspacesPage() {
  const {sessions, selected, select, now, planning, requestPlans} = useConsole();
  return <section className="route-page">
    <div className="toolbar">
      <span>{selected.size} sessions selected</span>
      <Button
        variant="outline"
        disabled={
          planning ||
          selected.size === 0 ||
          sessions
            .filter((s) => selected.has(s.identity.session_id))
            .some((s) => !controllable(s, now))
        }
        onClick={() =>
          void requestPlans(
            sessions
              .filter((s) => selected.has(s.identity.session_id))
              .map((session) => ({
                session,
                action: "close",
                targets: [],
              })),
          )
        }
      >
        {planning ? "Planning…" : "Review close selected"}
      </Button>
    </div>
    <Workspaces
      sessions={sessions}
      selected={selected}
      onSelect={select}
      now={now}
    />
  </section>;
}
export function WorkspacePage() {
  const {current, sessionID, data, sessions, now, status, requestPlans, tab, logTarget, onTabChange, onTargetChange} = useConsole();
  return (
    <section className="route-page">
      {current ? (
        <WorkspaceDetail
          key={sessionID}
          session={current}
          resources={data.resources}
          sessions={sessions}
          now={now}
          connected={status === "Live"}
          onPlan={requestPlans}
          tab={tab}
          target={logTarget ?? ""}
          onTabChange={onTabChange}
          onTargetChange={onTargetChange}
        />
      ) : (
        <Notice title="This session ended or disappeared">
          Session {sessionID} is no longer discovered. Select a current
          session explicitly; old operation outcomes remain in Operations.
        </Notice>
      )}
    </section>
  );
}

export function ResourcesPage() {
  const {data, sessions} = useConsole();
  return <Resources resources={data.resources} sessions={sessions} />;
}
export function OperationsPage() {
  const {sessions, tracked, setError} = useConsole();
  return <Operations sessions={sessions} tracked={tracked} onError={setError} />;
}
export function NotFoundPage() {
  return <Notice title="Page not found">This address does not match a console page. <Link to="/">Go to Overview</Link></Notice>;
}
