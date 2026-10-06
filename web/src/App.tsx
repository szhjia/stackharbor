import { useCallback, useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Anchor,
  Layers,
  Network,
  Activity,
  LayoutDashboard,
  ArrowLeft,
  RefreshCw,
} from "lucide-react";
import { api, ApiFailure, actionableError, newKey } from "./api/client";
import { controllable } from "./api/types";
import { useEvents } from "./api/events";
import { Workspaces } from "./pages/Workspaces";
import { Resources } from "./pages/Resources";
import { Operations, type Tracked } from "./pages/Operations";
import { WorkspaceDetail, type ActionRequest } from "./pages/WorkspaceDetail";
import { PlanDialog, type Planned } from "./components/PlanDialog";
import { Notice } from "./components/Notice";
import { memory, cpu } from "./components/MetricView";
import { Status } from "./components/Status";
import { Button } from "./components/ui/button";
import {
  NavigationMenu,
  NavigationMenuList,
  NavigationMenuItem,
  NavigationMenuLink,
} from "./components/ui/navigation-menu";
import { Skeleton } from "./components/ui/skeleton";
const navigation = [
  { name: "Overview", icon: LayoutDashboard },
  { name: "Workspaces", icon: Layers },
  { name: "Resources", icon: Network },
  { name: "Operations", icon: Activity },
] as const;
export function App() {
  const cache = useQueryClient();
  const [auth, setAuth] = useState<"pending" | "ready" | "expired">("pending");
  const [authError, setAuthError] = useState("");
  const authStarted = useRef(false);
  const [page, setPage] = useState("Overview");
  const [sessionID, setSessionID] = useState<string>();
  const [logTarget, setLogTarget] = useState<string>();
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [now, setNow] = useState(Date.now());
  const [plans, setPlans] = useState<Planned[]>();
  const [planning, setPlanning] = useState(false);
  const planningRef = useRef(false);
  const [error, setError] = useState("");
  const [results, setResults] = useState<string[]>([]);
  const [tracked, setTracked] = useState<Tracked[]>([]);
  useEffect(() => {
    if (authStarted.current) return;
    authStarted.current = true;
    void api
      .bootstrap(new URL(location.href), (path) =>
        history.replaceState(null, "", path),
      )
      .then(() => setAuth("ready"))
      .catch((e) => {
        setAuthError(actionableError(e));
        setAuth("expired");
      });
  }, []);
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);
  const inventory = useQuery({
    queryKey: ["inventory"],
    queryFn: () => api.inventory(),
    enabled: auth === "ready",
    retry: (n, e) => !(e instanceof ApiFailure && e.status === 401) && n < 2,
    refetchInterval: 3000,
  });
  const expire = useCallback(() => {
    setAuth("expired");
    setAuthError(
      "Browser session expired. Run stackharbor web again to authenticate.",
    );
    cache.clear();
  }, [cache]);
  useEffect(() => {
    if (inventory.error instanceof ApiFailure && inventory.error.status === 401)
      expire();
  }, [inventory.error, expire]);
  const status = useEvents(
    auth === "ready",
    inventory.data?.event_cursor,
    sessionID !== undefined && logTarget !== undefined
      ? { session: sessionID, target: logTarget }
      : undefined,
    expire,
  );
  const data = inventory.data;
  const sessions = data?.sessions ?? [];
  const current = sessions.find((s) => s.identity.session_id === sessionID);
  const open = useCallback((id: string) => {
    setSessionID(id);
    setPage("Workspaces");
    setSelected(new Set());
  }, []);
  const select = useCallback(
    (id: string, v: boolean) =>
      setSelected((old) => {
        const n = new Set(old);
        if (v) n.add(id);
        else n.delete(id);
        return n;
      }),
    [],
  );
  const requestPlans = useCallback(async (requests: ActionRequest[]) => {
    if (planningRef.current || requests.length === 0) return;
    planningRef.current = true;
    setPlanning(true);
    setError("");
    setResults([]);
    try {
      if (requests.some((r) => !controllable(r.session)))
        throw new Error("Refresh this session before planning.");
      const out = await Promise.allSettled(
        requests.map(async (r) => ({
          root: r.session.root,
          plan: await api.plan(
            r.session.identity.session_id,
            r.action,
            r.targets,
            r.port,
          ),
        })),
      );
      const valid: Planned[] = [];
      const failures: string[] = [];
      out.forEach((r, i) => {
        if (r.status === "fulfilled") valid.push(r.value);
        else
          failures.push(
            requests[i].session.root + ": " + actionableError(r.reason),
          );
      });
      if (failures.length) setError(failures.join(" · "));
      if (valid.length) setPlans(valid);
    } catch (e) {
      setError(actionableError(e));
    } finally {
      planningRef.current = false;
      setPlanning(false);
    }
  }, []);
  async function submit() {
    if (!plans) return;
    const outcomes = await Promise.allSettled(
      plans.map(async (p) => {
        const op = await api.submit(p.plan, newKey(p.plan));
        setTracked((old) => [
          ...old.filter((t) => t.id !== op.id),
          { session_id: op.session_id, id: op.id },
        ]);
        return p.root + ": operation " + op.id + " accepted (" + op.state + ")";
      }),
    );
    const lines = outcomes.map((r, i) =>
      r.status === "fulfilled"
        ? r.value
        : plans[i].root + ": " + actionableError(r.reason),
    );
    setResults(lines);
    void cache.invalidateQueries({ queryKey: ["operations"] });
    void cache.invalidateQueries({ queryKey: ["inventory"] });
    if (outcomes.some((r) => r.status === "rejected")) {
      throw new Error(
        "Some requests require attention. Each session outcome is listed below. " +
          lines.join(" · "),
      );
    }
    setPage("Operations");
    setSessionID(undefined);
  }
  if (auth !== "ready")
    return (
      <main className="auth-page">
        <Anchor />
        <h1>StackHarbor</h1>
        {auth === "pending" ? (
          <>
            <p>Authenticating this browser…</p>
            <Skeleton className="h-8 w-64" />
          </>
        ) : (
          <Notice title="Authenticate this browser" danger>
            <p>{authError}</p>
            <p>
              Run <code>stackharbor web</code> in your terminal and open the new
              link. Launch credentials expire after 60 seconds.
            </p>
          </Notice>
        )}
      </main>
    );
  return (
    <div className="console">
      <a className="skip-link" href="#main">
        Skip to content
      </a>
      <aside className="sidebar">
        <a
          href="/"
          className="brand"
          onClick={(e) => {
            e.preventDefault();
            setPage("Overview");
            setSessionID(undefined);
          }}
        >
          <Anchor />
          <span>
            StackHarbor<small>LOCAL CONTROL</small>
          </span>
        </a>
        <NavigationMenu viewport={false} className="console-nav">
          <NavigationMenuList>
            {navigation.map((item) => (
              <NavigationMenuItem key={item.name}>
                <NavigationMenuLink asChild>
                  <button
                    aria-label={item.name}
                    aria-current={page === item.name ? "page" : undefined}
                    onClick={() => {
                      setPage(item.name);
                      setSessionID(undefined);
                      setLogTarget(undefined);
                    }}
                  >
                    <item.icon />
                    <span>{item.name}</span>
                    {item.name === "Workspaces" ? (
                      <small>{sessions.length}</small>
                    ) : null}
                  </button>
                </NavigationMenuLink>
              </NavigationMenuItem>
            ))}
          </NavigationMenuList>
        </NavigationMenu>
        <div className="sidebar-bottom">
          <span className="caption">Current user · loopback</span>
          <span className="mono caption">{location.host}</span>
          <p>
            Sessions stay in their terminals. The console follows their state.
          </p>
        </div>
      </aside>
      <main id="main" className="main">
        <header className="page-header">
          <div>
            <p className="eyebrow">FOREGROUND WORKSPACES</p>
            <h1>{sessionID ? "Workspace detail" : page}</h1>
            <p className="page-intro">
              {page === "Overview"
                ? "Connected sessions and the resources they share."
                : page === "Resources"
                  ? "Physical identities, owners and consumers."
                  : page === "Operations"
                    ? "Independent outcomes for every accepted action."
                    : "Inspect live nodes and plan deliberate changes."}
            </p>
          </div>
          <div className="connection">
            <Status value={status} />
            <Button variant="outline" onClick={() => void inventory.refetch()}>
              <RefreshCw data-icon="inline-start" />
              Refresh
            </Button>
          </div>
        </header>
        {error ? (
          <Notice title="Request requires attention" danger>
            {error}
          </Notice>
        ) : null}
        {results.length ? (
          <Notice title="Per-session requests">
            {results.map((r, i) => (
              <p key={i}>{r}</p>
            ))}
          </Notice>
        ) : null}
        {data?.collection_error ? (
          <Notice title="Inventory discovery unavailable">
            {data.collection_error.message}. Retained records and totals may be
            incomplete.
          </Notice>
        ) : data?.partial ? (
          <Notice title="Partial observation">
            Some sessions or metrics are unavailable. Retained observations may
            be stale.
          </Notice>
        ) : null}
        {inventory.error ? (
          <Notice title="Inventory unavailable">
            {actionableError(inventory.error)}. Retrying discovery.
          </Notice>
        ) : null}
        {!data ? (
          <section aria-label="Loading inventory">
            <Skeleton className="h-20 w-full" />
            <p>Waiting for the first successful inventory refresh…</p>
          </section>
        ) : sessionID ? (
          <>
            <Button
              variant="ghost"
              onClick={() => {
                setSessionID(undefined);
                setLogTarget(undefined);
              }}
            >
              <ArrowLeft data-icon="inline-start" />
              All workspaces
            </Button>
            {current ? (
              <WorkspaceDetail
                key={sessionID}
                session={current}
                resources={data.resources}
                sessions={sessions}
                now={now}
                connected={status === "Live"}
                onPlan={requestPlans}
                onLogFilter={setLogTarget}
                onOpen={open}
              />
            ) : (
              <Notice title="This session ended or disappeared">
                Session {sessionID} is no longer discovered. Select a current
                session explicitly; old operation outcomes remain in Operations.
              </Notice>
            )}
          </>
        ) : (
          <>
            {page === "Overview" ? (
              <>
                <section className="observation-summary">
                  <div>
                    <span className="caption">SESSION COVERAGE</span>
                    <strong>
                      {sessions.filter((s) => controllable(s, now)).length}
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
                <section className="section-heading">
                  <h2>Workspace connections</h2>
                  <span className="caption">
                    Observed {new Date(data.collected_at).toLocaleTimeString()}
                  </span>
                </section>
                <Workspaces
                  sessions={sessions}
                  selected={selected}
                  onSelect={select}
                  onOpen={open}
                  now={now}
                />
                <section className="section-heading">
                  <h2>Resource ownership</h2>
                  <span className="caption">
                    Shared physical resources are counted once.
                  </span>
                </section>
                <Resources
                  resources={data.resources}
                  sessions={sessions}
                  onOpen={open}
                />
              </>
            ) : page === "Workspaces" ? (
              <>
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
                  onOpen={open}
                  now={now}
                />
              </>
            ) : page === "Resources" ? (
              <Resources
                resources={data.resources}
                sessions={sessions}
                onOpen={open}
              />
            ) : (
              <Operations
                sessions={sessions}
                tracked={tracked}
                onError={setError}
              />
            )}
          </>
        )}
        {planning ? (
          <p role="status">Planning against current session identities…</p>
        ) : null}
        {plans ? (
          <PlanDialog
            plans={plans}
            onSubmit={submit}
            onClose={() => setPlans(undefined)}
          />
        ) : null}
        <footer className="footer">
          StackHarbor · Local session control{" "}
          <span>
            {data
              ? `Observed ${new Date(data.collected_at).toLocaleTimeString()}`
              : "Discovery pending"}
          </span>
        </footer>
      </main>
    </div>
  );
}
