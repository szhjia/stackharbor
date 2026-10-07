import { usePreferences } from "../lib/preferences";
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
  const {t: tr, language} = usePreferences();
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
        <span className="caption">{tr("SESSION COVERAGE")}</span>
        <strong>
          {fresh}
          <small> / {sessions.length}{" "}{tr("fresh")}</small>
        </strong>
        <p>
          {data.partial ? tr("Partial inventory") : tr("Current inventory")}
        </p>
      </div>
      {(["processes", "containers"] as const).map((kind) => (
        <div key={kind}>
          <span className="caption">
            {kind === "processes"
              ? tr("PROCESS RSS")
              : tr("CONTAINER WORKING SET")}
          </span>
          <strong>{tr(memory(data.totals[kind].memory_bytes))}</strong>
          <p>
            {data.totals[kind].count} {tr(kind)}{" "}{tr("· CPU")}{" "}
            {tr(cpu(data.totals[kind].cpu_percent))}
            {data.totals[kind].partial ? tr(" · partial") : ""}
          </p>
        </div>
      ))}
    </section>
    <div className="overview-summaries">
      <SummaryCard id="workspace-summary-title" title={tr("Workspaces")} href="/workspaces" total={workspaceCount}
        unit={`${tr(workspaceCount === 1 ? "workspace" : "workspaces")} · ${sessions.length} ${tr(sessions.length === 1 ? "session" : "sessions")}`}
        items={[
          {label: tr("Fresh sessions"), value: fresh},
          {label: tr("Needs attention"), value: sessions.length - fresh - unavailable},
          {label: tr("Unavailable"), value: unavailable},
        ]}
        description={sessions.length === 0 ? tr("No foreground sessions") : tr("Needs attention includes stale snapshots and unsupported protocols.")} />
      <SummaryCard id="resource-summary-title" title={tr("Resources")} href="/resources" total={resources.length} unit={tr("physical resources")}
        items={[
          {label: tr("Processes"), value: resources.filter(r => r.kind === "process").length},
          {label: tr("Containers"), value: resources.filter(r => r.kind === "container").length},
          {label: tr("Shared"), value: shared},
          {label: tr("Unknown identity"), value: resources.filter(r => !r.identity_known).length},
        ]}
        description={resources.length === 0 ? tr("No observed resources") : tr("Each physical resource is counted once. Shared resources serve multiple nodes.")} />
    </div>
    <p className="caption">{tr("Observed")}{" "}{new Date(data.collected_at).toLocaleTimeString(language)}{data.partial ? tr(" · Partial inventory; retained observations may be stale.") : ""}</p>
  </section>;
}
export function WorkspacesPage() {
  const {t: tr, language} = usePreferences();
  const {sessions, selected, select, now, planning, requestPlans} = useConsole();
  return <section className="route-page">
    <div className="toolbar">
      <span>{tr("{count} sessions selected", {count: selected.size})}</span>
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
        {planning ? tr("Planning…") : tr("Review close selected")}
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
  const {t: tr, language} = usePreferences();
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
        <Notice title={tr("This session ended or disappeared")}>{tr("Session")}{" "}{sessionID}{" "}{tr("is no longer discovered. Select a current session explicitly; old operation outcomes remain in Operations.")}{" "}</Notice>
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
  const {t: tr, language} = usePreferences();
  return <Notice title={tr("Page not found")}>{tr("This address does not match a console page.")}{" "}<Link to="/">{tr("Go to Overview")}</Link></Notice>;
}
