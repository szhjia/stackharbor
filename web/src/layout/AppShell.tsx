import { matchRoutes, Outlet, useLocation, useNavigate, useOutletContext, useSearchParams } from "react-router";
import { useCallback, useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import brandMark from "../assets/stackharbor-favicon.png";
import { consoleRoutes, workspacePath } from "../routes";
import { api, ApiFailure, actionableError, newKey } from "../api/client";
import { controllable, type Inventory, type Session } from "../api/types";
import { useEvents } from "../api/events";
import type { ActionRequest } from "../pages/WorkspaceDetail";
import type { Tracked } from "../pages/Operations";
import { PlanDialog, type Planned } from "../components/PlanDialog";
import { Notice } from "../components/Notice";
import { Button } from "../components/ui/button";
import { Skeleton } from "../components/ui/skeleton";
import { Sidebar } from "./Sidebar";
import { AppHeader } from "./AppHeader";
import { AppContent } from "./AppContent";

export interface ConsoleContext {
  data: Inventory;
  sessions: Session[];
  current?: Session;
  sessionID?: string;
  now: number;
  status: string;
  selected: Set<string>;
  select: (id: string, value: boolean) => void;
  planning: boolean;
  requestPlans: (requests: ActionRequest[]) => Promise<void>;
  tracked: Tracked[];
  setError: (error: string) => void;
  tab: string;
  logTarget?: string;
  onTabChange: (tab: string) => void;
  onTargetChange: (target: string) => void;
}
export function useConsole() {
  return useOutletContext<ConsoleContext>();
}
export function AppShell() {
  const navigate = useNavigate();
  const route = useLocation();
  const matched = matchRoutes(consoleRoutes, route)?.at(-1);
  const page = matched?.route.handle.page ?? "Page not found";
  const tab = matched?.route.handle.tab ?? "services";
  const sessionID = matched?.params.sessionID;
  const [search, setSearch] = useSearchParams();
  const logTarget = sessionID && tab === "logs" ? search.get("target") ?? "" : undefined;
  const cache = useQueryClient();
  const [sidebarCollapsed, setSidebarCollapsed] = useState(() => {
    try { return localStorage.getItem("stackharbor.sidebar-collapsed") === "true"; }
    catch { return false; }
  });
  function toggleSidebar() {
    const next = !sidebarCollapsed;
    setSidebarCollapsed(next);
    try { localStorage.setItem("stackharbor.sidebar-collapsed", String(next)); }
    catch { /* Layout remains usable when browser storage is unavailable. */ }
  }
  const [auth, setAuth] = useState<"pending" | "ready" | "expired">("pending");
  const [authError, setAuthError] = useState("");
  const authStarted = useRef(false);
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
        navigate(path, {replace: true}),
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
      "Local connection could not be restored. Try connecting again.",
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
  useEffect(() => {
    setSelected(new Set());
  }, [page, sessionID]);
  useEffect(() => {
    const workspace = current?.root.split("/").filter(Boolean).at(-1);
    const section = tab === "services" ? "" : `${tab[0].toUpperCase() + tab.slice(1)} · `;
    document.title = `${workspace ? section + workspace : page} · StackHarbor`;
  }, [page, current?.root, tab]);
  useEffect(() => {
    document.getElementById("main")?.scrollIntoView?.({block: "start"});
  }, [route.pathname]);
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
    navigate("/operations");
  }
  if (auth !== "ready")
    return (
      <main className="auth-page">
        <img src={brandMark} alt="" className="auth-mark" />
        <h1>StackHarbor</h1>
        {auth === "pending" ? (
          <>
            <p>Connecting to local control…</p>
            <Skeleton className="h-8 w-64" />
          </>
        ) : (
          <Notice title="Local connection unavailable" danger>
            <p>{authError}</p>
            <p>
              Keep <code>stackharbor web</code> running in your terminal.
            </p>
            <Button onClick={() => {
              setAuth("pending");
              void api.session().then(() => setAuth("ready")).catch((e) => {
                setAuthError(actionableError(e));
                setAuth("expired");
              });
            }}>Connect again</Button>
          </Notice>
        )}
      </main>
    );
  const context: ConsoleContext | undefined = data ? {
    data, sessions, current, sessionID, now, status, selected, select,
    planning, requestPlans, tracked, setError, tab, logTarget,
    onTabChange: (next) => { if (sessionID) navigate(workspacePath(sessionID, next)); },
    onTargetChange: (target) => setSearch(target ? {target} : {}, {replace: true}),
  } : undefined;
  return (
    <div className={sidebarCollapsed ? "console sidebar-collapsed" : "console"}>
      <a className="skip-link" href="#app-content">Skip to content</a>
      <Sidebar page={page} sessionCount={sessions.length} collapsed={sidebarCollapsed} />
      <main id="main" className="main">
        <AppHeader sidebarCollapsed={sidebarCollapsed} onToggleSidebar={toggleSidebar} page={page} sessionID={sessionID} workspace={current?.root.split("/").filter(Boolean).at(-1)} tab={tab} status={status} refreshing={inventory.isFetching} onRefresh={() => void inventory.refetch()} />
        <AppContent>
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
          {!context ? (
            <section aria-label="Loading inventory">
              <Skeleton className="h-20 w-full" />
              <p>Waiting for the first successful inventory refresh…</p>
            </section>
          ) : <Outlet context={context} />}
          {planning ? <p role="status">Planning against current session identities…</p> : null}
        </AppContent>
        {plans ? <PlanDialog plans={plans} onSubmit={submit} onClose={() => setPlans(undefined)} /> : null}
      </main>
    </div>
  );
}
