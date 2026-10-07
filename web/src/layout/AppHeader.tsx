import { usePreferences } from "../lib/preferences";
import { Link } from "react-router";
import { ChevronRight, PanelLeftClose, PanelLeftOpen, RefreshCw } from "lucide-react";
import { workspacePath } from "../routes";
import { Status } from "../components/Status";
import { Button } from "../components/ui/button";

export function AppHeader({sidebarCollapsed, onToggleSidebar, page, sessionID, workspace, tab, status, refreshing, onRefresh}: {
  sidebarCollapsed: boolean;
  onToggleSidebar: () => void;
  page: string;
  sessionID?: string;
  workspace?: string;
  tab: string;
  status: string;
  refreshing: boolean;
  onRefresh: () => void;
}) {
  const {t: tr} = usePreferences();
  const section = tab[0].toUpperCase() + tab.slice(1);
  return (
    <header className="app-header">
      <div className="app-header-start">
        <Button variant="ghost" size="icon" className="sidebar-toggle"
          aria-label={sidebarCollapsed ? tr("Expand sidebar") : tr("Collapse sidebar")}
          title={sidebarCollapsed ? tr("Expand sidebar") : tr("Collapse sidebar")}
          aria-controls="app-sidebar" aria-expanded={!sidebarCollapsed}
          onClick={onToggleSidebar}>
          {sidebarCollapsed ? <PanelLeftOpen /> : <PanelLeftClose />}
        </Button>
      <nav aria-label={tr("Breadcrumb")} className="app-breadcrumb">
        <ol>
          {sessionID ? <>
            <li><Link to="/workspaces" aria-label={tr("All workspaces")}>{tr("Workspaces")}</Link></li>
            <li aria-hidden="true"><ChevronRight /></li>
            <li>{tab === "services"
              ? <h1 aria-current="page">{workspace || tr("Workspace detail")}</h1>
              : <Link to={workspacePath(sessionID)}>{workspace || tr("Workspace detail")}</Link>}
            </li>
            {tab !== "services" ? <>
              <li aria-hidden="true"><ChevronRight /></li>
              <li><h1 aria-current="page">{tr(section)}</h1></li>
            </> : null}
          </> : <li><h1 aria-current="page">{tr(page)}</h1></li>}
        </ol>
      </nav>
      </div>
      <div className="connection">
        <Status value={status} />
        <Button variant="outline" disabled={refreshing} onClick={onRefresh}>
          <RefreshCw data-icon="inline-start" className={refreshing ? "animate-spin" : undefined} />{tr("Refresh")}{" "}</Button>
      </div>
    </header>
  );
}
