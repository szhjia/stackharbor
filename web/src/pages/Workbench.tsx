import { Link } from "react-router";
import { ArrowUpRight, Activity, Boxes, FolderOpen, MemoryStick } from "lucide-react";
import type { CSSProperties } from "react";
import { useConsole } from "../layout/AppShell";
import { usePreferences } from "../lib/preferences";
import { presentWorkbenchResource, summarizeWorkbench } from "../lib/workbench";
import { workspacePath } from "../routes";
import { sessionStale } from "../api/types";
import { memory, cpu } from "../components/MetricView";
import { Card, CardHeader, CardTitle, CardDescription, CardContent, CardFooter, CardAction } from "../components/ui/card";
import { Empty, EmptyHeader, EmptyTitle, EmptyDescription } from "../components/ui/empty";
import { Badge } from "../components/ui/badge";
import { CopyButton } from "../components/CopyButton";
import { useNow } from "../lib/use-now";

export function WorkbenchPage() {
  const {data} = useConsole();
  const now = useNow();
  const {t: tr, language} = usePreferences();
  const stats = summarizeWorkbench(data, now);
  const number = (value: number) => value.toLocaleString(language);
  if (data.sessions.length === 0 && data.resources.length === 0)
    return <section className="route-page workbench">
      <Card role="region" aria-labelledby="workbench-empty-title">
        <CardHeader><CardTitle><h2 id="workbench-empty-title">{tr("No foreground sessions")}</h2></CardTitle></CardHeader>
        <CardContent>
          <Empty><EmptyHeader><EmptyTitle>{tr("Ready when you are")}</EmptyTitle><EmptyDescription>{tr("Start a workspace session in your terminal to see its status here.")}</EmptyDescription></EmptyHeader></Empty>
          <div className="onboarding-command"><code>stackharbor --root /path/to/workspace</code><CopyButton value="stackharbor --root /path/to/workspace" label="Copy command" /></div>
          <p className="caption">{tr("Replace /path/to/workspace with your workspace path and keep that terminal open.")}</p>
        </CardContent>
        <CardFooter><Link className="workbench-link" to="/workspaces">{tr("Open workspaces")}<ArrowUpRight aria-hidden="true" /></Link></CardFooter>
      </Card>
    </section>;
  const health = [
    {name: tr("Fresh sessions"), value: stats.fresh, fill: "var(--primary)"},
    {name: tr("Snapshots need review"), value: stats.needsAttention, fill: "var(--caution)"},
    {name: tr("Unavailable"), value: stats.unavailable, fill: "var(--destructive)"},
  ];
  const topMemory = stats.topMemory;
  const ranking = topMemory.map((resource, index) => ({
    name: `${index + 1}`,
    presentation: presentWorkbenchResource(resource, data.sessions),
    resource,
    share: topMemory[0].metric.rss_bytes! > 0 ? Math.round((resource.metric.rss_bytes! / topMemory[0].metric.rss_bytes!) * 100) : 0,
  }));
  const metrics = [
    {label: tr("Workspaces"), value: number(stats.workspaceCount), detail: tr("{count} discovered sessions", {count: data.sessions.length}), icon: FolderOpen, href: "/workspaces"},
    {label: tr("Runtime resources"), value: number(data.resources.length), detail: tr("{processes} processes · {containers} containers", {processes: stats.processes, containers: stats.containers}), icon: Boxes, href: "/resources"},
    {label: tr("Process RSS"), value: tr(memory(data.totals.processes.memory_bytes)), detail: `${tr("CPU")} ${tr(cpu(data.totals.processes.cpu_percent))}${data.totals.processes.partial ? tr(" · partial") : ""}`, icon: Activity, href: "/resources?kind=process"},
    {label: tr("Container working set"), value: tr(memory(data.totals.containers.memory_bytes)), detail: `${tr("CPU")} ${tr(cpu(data.totals.containers.cpu_percent))}${data.totals.containers.partial ? tr(" · partial") : ""}`, icon: MemoryStick, href: "/resources?kind=container"},
  ];
  const issues = [
    ...stats.attention.map(session => ({session, node: undefined, reason: tr(!session.available ? "Unavailable" : sessionStale(session, now) ? "stale" : "Unsupported protocol")})),
    ...stats.runtimeIssues,
  ];
  return <section className="route-page workbench">
    <div className="workbench-metrics">
      {metrics.map(({label, value, detail, icon: Icon, href}) => <Link key={label} className="workbench-metric" to={href}>
        <Card size="sm">
          <CardHeader><CardDescription>{label}</CardDescription><CardAction><Icon aria-hidden="true" /></CardAction></CardHeader>
          <CardContent><strong className="workbench-stat">{value}</strong><p className="caption">{detail}</p></CardContent>
        </Card>
      </Link>)}
    </div>
    {issues.length ? <Card role="region" aria-labelledby="attention-title" className="attention-card">
      <CardHeader><CardTitle><h2 id="attention-title">{tr("Needs your attention")}</h2></CardTitle><CardDescription>{tr("Connection issues, current node failures and port conflicts.")}</CardDescription><CardAction><Badge variant="destructive">{number(issues.length)}</Badge></CardAction></CardHeader>
      <CardContent><ul className="workbench-attention">{issues.slice(0, 5).map(({session, node, reason}, index) => <li key={`${session.identity.session_id}/${node?.id ?? index}`}>
        <Link to={workspacePath(session.identity.session_id, node?.kind === "task" ? "tasks" : "services") + (node ? `?q=${encodeURIComponent(node.id)}` : "")}>
          <span><strong>{node?.name || node?.id || session.root.split("/").filter(Boolean).at(-1) || session.root}</strong><small className="caption">{session.root}{node ? ` · ${tr(node.kind === "task" ? "Task" : "Service")}` : ""}</small><span className="attention-reason">{reason}</span></span>
          {node ? <Badge variant="destructive">{tr(node.state)}</Badge> : null}<ArrowUpRight aria-hidden="true" />
        </Link>
      </li>)}</ul></CardContent>
      <CardFooter><Link className="workbench-link" to="/workspaces">{tr("Open workspaces")}<ArrowUpRight aria-hidden="true" /></Link></CardFooter>
    </Card> : null}
    <div className="workbench-secondary">
      <Card role="region" aria-labelledby="memory-ranking-title" className="workbench-memory-card">
        <CardHeader><CardTitle><h2 id="memory-ranking-title">{tr("Memory hotspots")}</h2></CardTitle><CardDescription>{tr("Top 5 physical resources · current sample · MiB")}</CardDescription><CardAction><Badge variant="secondary">{tr("Snapshot")}</Badge></CardAction></CardHeader>
        <CardContent className="workbench-memory-content" role="region" aria-label={tr("Memory ranking list")} tabIndex={0}>
          {ranking.length ? <ol className="workbench-ranking" aria-label={tr("Memory hotspots")}>
            {ranking.map(({resource, presentation, name, share}) => <li key={resource.id} style={{"--bar": `${share}%`} as CSSProperties}>
              <span className="mono workbench-rank-number">{name}</span>
              <div className="workbench-rank-main">
                <div className="workbench-rank-title">
                  <Link className="workbench-resource-name" title={presentation.fullTitle || undefined} to={`/resources?q=${encodeURIComponent(resource.id)}`}>{presentation.title || tr("Unassigned resource")}</Link>
                  {presentation.additionalAssociations ? <span className="workbench-rank-count"><span aria-hidden="true">+{presentation.additionalAssociations}</span><span className="sr-only">{tr("{count} more associated nodes", {count: presentation.additionalAssociations})}</span></span> : null}
                </div>
                <small className="caption">{presentation.workspace || tr("Unknown")} · {tr(resource.kind === "process" ? "Process" : resource.kind === "container" ? "Container" : resource.kind)} · <span className="mono">{presentation.identity || tr("Unknown identity")}</span>{resource.metric.partial ? ` · ${tr("Partial sample")}` : ""}</small>
                <span className="workbench-rank-track" aria-hidden="true"><span className="workbench-rank-bar" /></span>
              </div>
              <strong className="mono workbench-rank-value">{tr(memory(resource.metric.rss_bytes))}</strong>
            </li>)}
          </ol> : <Empty><EmptyHeader><EmptyTitle>{tr("No memory samples yet")}</EmptyTitle><EmptyDescription>{tr("Measured resources will appear here. Unknown values are not counted as zero.")}</EmptyDescription></EmptyHeader></Empty>}
        </CardContent>
        <CardFooter><p className="caption">{tr("{count} resources without memory samples", {count: stats.unknownMemory})} · {tr("RSS and working set are different measurements.")}</p></CardFooter>
      </Card>
      <Card role="region" aria-labelledby="session-health-title">
        <CardHeader><CardTitle><h2 id="session-health-title">{tr("Session health")}</h2></CardTitle><CardDescription>{tr("Fresh snapshots with a supported protocol")}</CardDescription></CardHeader>
        <CardContent>
          <dl className="workbench-health">{health.map(item => <div key={item.name}><dt><span aria-hidden="true" style={{backgroundColor: item.fill}} />{item.name}</dt><dd>{number(item.value)}</dd></div>)}</dl>
        </CardContent>
        <CardFooter><Link className="workbench-link" to="/workspaces">{tr("All workspaces")}<ArrowUpRight aria-hidden="true" /></Link></CardFooter>
      </Card>
    </div>
  </section>;
}
