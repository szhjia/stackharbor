import { Link } from "react-router";
import { ArrowUpRight, Activity, Boxes, FolderOpen, MemoryStick } from "lucide-react";
import { Bar, BarChart, CartesianGrid, Pie, PieChart, XAxis, YAxis } from "recharts";
import { useConsole } from "../layout/AppShell";
import { usePreferences } from "../lib/preferences";
import { summarizeWorkbench } from "../lib/workbench";
import { workspacePath } from "../routes";
import { sessionStale } from "../api/types";
import { memory, cpu } from "../components/MetricView";
import { Card, CardHeader, CardTitle, CardDescription, CardContent, CardFooter, CardAction } from "../components/ui/card";
import { ChartContainer, ChartTooltip, ChartTooltipContent } from "../components/ui/chart";
import { Empty, EmptyHeader, EmptyTitle, EmptyDescription } from "../components/ui/empty";
import { Badge } from "../components/ui/badge";

export function WorkbenchPage() {
  const {data, now} = useConsole();
  const {t: tr, language} = usePreferences();
  const stats = summarizeWorkbench(data, now);
  const number = (value: number) => value.toLocaleString(language);
  const health = [
    {name: tr("Fresh sessions"), value: stats.fresh, fill: "var(--chart-1)"},
    {name: tr("Needs attention"), value: stats.needsAttention, fill: "var(--caution)"},
    {name: tr("Unavailable"), value: stats.unavailable, fill: "var(--destructive)"},
  ];
  const ranking = stats.topMemory.map((r, index) => ({
    name: `${index + 1}`, value: r.metric.rss_bytes! / 1048576,
    label: r.references.flatMap(ref => ref.node_ids).filter((v, i, a) => a.indexOf(v) === i).join(", ") || r.id,
    resource: r,
  }));
  const metrics = [
    {label: "Workspaces", value: number(stats.workspaceCount), detail: tr("{count} discovered sessions", {count: data.sessions.length}), icon: FolderOpen, href: "/workspaces"},
    {label: "Runtime resources", value: number(data.resources.length), detail: tr("{processes} processes · {containers} containers", {processes: stats.processes, containers: stats.containers}), icon: Boxes, href: "/resources"},
    {label: "PROCESS RSS", value: tr(memory(data.totals.processes.memory_bytes)), detail: `${tr("CPU")} ${tr(cpu(data.totals.processes.cpu_percent))}${data.totals.processes.partial ? tr(" · partial") : ""}`, icon: Activity, href: "/resources"},
    {label: "CONTAINER WORKING SET", value: tr(memory(data.totals.containers.memory_bytes)), detail: `${tr("CPU")} ${tr(cpu(data.totals.containers.cpu_percent))}${data.totals.containers.partial ? tr(" · partial") : ""}`, icon: MemoryStick, href: "/resources"},
  ];
  return <section className="route-page workbench">
    <div className="workbench-intro">
      <div><h2>{tr("Your local runtime, at a glance")}</h2><p>{tr("Inspect sessions, find resource hotspots, and pick up where attention is needed.")}</p></div>
    </div>
    <div className="workbench-metrics">
      {metrics.map(({label, value, detail, icon: Icon, href}) => <Card key={label} size="sm">
        <CardHeader><CardDescription>{tr(label)}</CardDescription><CardAction><Icon aria-hidden="true" /></CardAction></CardHeader>
        <CardContent><strong className="workbench-stat">{value}</strong><p className="caption">{detail}</p></CardContent>
        <CardFooter><Link className="workbench-link" to={href} aria-label={`${tr("Inspect")} · ${tr(label)}`}>{tr("Inspect")}<ArrowUpRight aria-hidden="true" /></Link></CardFooter>
      </Card>)}
    </div>
    <div className="workbench-charts">
      <Card role="region" aria-labelledby="memory-ranking-title">
        <CardHeader><CardTitle><h2 id="memory-ranking-title">{tr("Memory hotspots")}</h2></CardTitle><CardDescription>{tr("Top 5 physical resources · current sample · MiB")}</CardDescription><CardAction><Badge variant="secondary">{tr("Snapshot")}</Badge></CardAction></CardHeader>
        <CardContent>
          {ranking.length ? <>
            <ChartContainer className="workbench-bar-chart" config={{value: {label: "MiB", color: "var(--chart-1)"}}}>
              <BarChart accessibilityLayer data={ranking} layout="vertical" margin={{left: 0, right: 16, top: 8, bottom: 0}}>
                <CartesianGrid horizontal={false} />
                <XAxis tick={{fill: "var(--muted-foreground)"}} type="number" tickLine={false} axisLine={false} tickFormatter={v => number(v)} />
                <YAxis tick={{fill: "var(--muted-foreground)"}} type="category" dataKey="name" tickLine={false} axisLine={false} width={24} />
                <ChartTooltip cursor={false} content={<ChartTooltipContent hideLabel formatter={(value, _name, item) => <span>{item.payload.label}: {Number(value).toLocaleString(language, {maximumFractionDigits: 1})} MiB</span>} />} />
                <Bar dataKey="value" fill="var(--chart-1)" radius={4} isAnimationActive={false} />
              </BarChart>
            </ChartContainer>
            <ol className="workbench-ranking" aria-label={tr("Memory hotspots")}>
              {ranking.map(({resource, label, name}) => <li key={resource.id}>
                <span className="mono">{name}</span><div><span className="workbench-resource-name" title={label}>{label}</span><small className="caption">{tr(resource.kind === "process" ? "Process" : resource.kind === "container" ? "Container" : resource.kind)} · <span className="mono">{resource.id}</span>{resource.metric.partial ? ` · ${tr("Partial sample")}` : ""}</small></div><strong className="mono">{tr(memory(resource.metric.rss_bytes))}</strong>
              </li>)}
            </ol>
          </> : <Empty><EmptyHeader><EmptyTitle>{tr("No memory samples yet")}</EmptyTitle><EmptyDescription>{tr("Measured resources will appear here. Unknown values are not counted as zero.")}</EmptyDescription></EmptyHeader></Empty>}
        </CardContent>
        <CardFooter><p className="caption">{tr("{count} resources without memory samples", {count: stats.unknownMemory})} · {tr("RSS and working set are different measurements.")}</p></CardFooter>
      </Card>
      <Card role="region" aria-labelledby="session-health-title">
        <CardHeader><CardTitle><h2 id="session-health-title">{tr("Session health")}</h2></CardTitle><CardDescription>{tr("Fresh snapshots with a supported protocol")}</CardDescription></CardHeader>
        <CardContent>
          {data.sessions.length ? <div className="workbench-health">
            <ChartContainer className="workbench-donut" config={{value: {label: tr("sessions")}}}>
              <PieChart accessibilityLayer><ChartTooltip content={<ChartTooltipContent hideLabel />} /><Pie data={health.filter(h => h.value > 0)} dataKey="value" nameKey="name" innerRadius="72%" outerRadius="95%" strokeWidth={3} isAnimationActive={false} /></PieChart>
            </ChartContainer>
            <div className="workbench-health-center" aria-hidden="true"><strong>{number(stats.fresh)}</strong><span>{tr("fresh")} / {number(data.sessions.length)}</span></div>
          </div> : <Empty><EmptyHeader><EmptyTitle>{tr("No foreground sessions")}</EmptyTitle><EmptyDescription>{tr("Start a workspace session in your terminal to see its status here.")}</EmptyDescription></EmptyHeader></Empty>}
          <dl className="workbench-health-legend">{health.map(item => <div key={item.name}><dt><span aria-hidden="true" style={{backgroundColor: item.fill}} />{item.name}</dt><dd>{number(item.value)}</dd></div>)}</dl>
        </CardContent>
        <CardFooter><Link className="workbench-link" to="/workspaces">{tr("All workspaces")}<ArrowUpRight aria-hidden="true" /></Link></CardFooter>
      </Card>
    </div>
    <Card role="region" aria-labelledby="attention-title">
      <CardHeader><CardTitle><h2 id="attention-title">{tr("Needs your attention")}</h2></CardTitle><CardDescription>{tr("Unavailable, stale, or unsupported sessions. Open a session to inspect it.")}</CardDescription><CardAction><Badge variant={stats.attention.length ? "destructive" : "secondary"}>{number(stats.attention.length)}</Badge></CardAction></CardHeader>
      <CardContent>{stats.attention.length ? <ul className="workbench-attention">{stats.attention.slice(0, 5).map(s => <li key={s.identity.session_id}>
        <Link to={workspacePath(s.identity.session_id)}><span><strong>{s.root.split("/").filter(Boolean).at(-1) || s.root}</strong><small className="caption">{s.root} · {s.identity.session_id}</small></span><Badge variant="secondary">{tr(!s.available ? "Unavailable" : sessionStale(s, now) ? "stale" : "Unsupported protocol")}</Badge><ArrowUpRight aria-hidden="true" /></Link>
      </li>)}</ul> : <Empty><EmptyHeader><EmptyTitle>{tr(data.sessions.length ? "All sessions are up to date" : "Ready when you are")}</EmptyTitle><EmptyDescription>{tr(data.sessions.length ? "No connection or freshness issues in the current inventory." : "Keep your workspace terminal open; discovered sessions appear automatically.")}</EmptyDescription></EmptyHeader></Empty>}</CardContent>
      <CardFooter><Link className="workbench-link" to="/workspaces">{tr(stats.attention.length > 5 ? "View all sessions needing attention" : "Open workspaces")}<ArrowUpRight aria-hidden="true" /></Link></CardFooter>
    </Card>

  </section>;
}
