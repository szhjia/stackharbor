import { usePreferences } from "../lib/preferences";
import { Link } from "react-router";
import { useConsole } from "../layout/AppShell";
import { Workspaces } from "./Workspaces";
import { Resources } from "./Resources";
import { Operations } from "./Operations";
import { WorkspaceDetail } from "./WorkspaceDetail";
import { Notice } from "../components/Notice";

export function WorkspacesPage() {
  const {sessions, now} = useConsole();
  return <section className="route-page">
    <Workspaces
      sessions={sessions}
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
  return <Notice title={tr("Page not found")}>{tr("This address does not match a console page.")}{" "}<Link to="/">{tr("Go to Workbench")}</Link></Notice>;
}
