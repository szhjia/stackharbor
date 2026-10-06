import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { expect, it, vi } from "vitest";
import { WorkspaceDetail } from "../pages/WorkspaceDetail";
import type { Node, Session } from "../api/types";

const identity = {session_id: "exact", workspace_id: "workspace", protocol_version: 1, capabilities: []};
const node: Node = {id: "backend/api", name: "API", project_id: "backend", kind: "service", state: "running", reason: "",
  ownership: "session", allowed_actions: ["restart", "stop"], resource_refs: ["process:123:456"], depends_on: ["db"],
  metric: {known: true, partial: false, sampled_at: null, rss_bytes: 1048576, cpu_percent: 2, uptime_millis: null},
  ports: [{port: 5102, status: "owned", reason: "", resource_refs: []}]};
function fixture(): Session {
  return {identity, root: "/fixture", pid: 123, available: true, stale: false, started_at: new Date().toISOString(), last_seen: null,
    snapshot: {identity, revision: 1, observed_at: new Date().toISOString(), nodes: [node], processes: [], containers: [], diagnostics: []}};
}
it("compact node rows expose only allowed actions, retain menus across refresh and request review rather than execute", async () => {
  const session = fixture();
  const onPlan = vi.fn();
  const cache = new QueryClient();
  const props = {session, resources: [], sessions: [session], now: Date.now(), connected: true, onPlan, tab: "services", target: "", onTabChange: vi.fn(), onTargetChange: vi.fn()};
  const view = (current = session) => <QueryClientProvider client={cache}><MemoryRouter><WorkspaceDetail {...props} session={current} /></MemoryRouter></QueryClientProvider>;
  const mounted = render(view());
  expect(screen.getAllByRole("columnheader").map(e => e.textContent)).toEqual(["Select", "Service", "State", "Ports", "CPU", "Memory", "Actions"]);
  expect(screen.getByRole("link", {name: "Logs for backend/api"})).toHaveAttribute("href", "/workspaces/exact/logs?target=backend%2Fapi");
  await userEvent.click(screen.getByRole("button", {name: "Actions for backend/api"}));
  expect(screen.queryByRole("menuitem", {name: "Start"})).not.toBeInTheDocument();
  mounted.rerender(view(structuredClone(session)));
  await userEvent.click(screen.getByRole("menuitem", {name: "Restart"}));
  expect(onPlan).toHaveBeenCalledWith([{session, action: "restart", targets: ["backend/api"]}]);
  await userEvent.click(screen.getByRole("button", {name: "Actions for backend/api"}));
  mounted.rerender(view({...session, stale: true}));
  expect(screen.getByRole("menuitem", {name: "Stop"})).toHaveAttribute("aria-disabled", "true");
  await userEvent.click(screen.getByRole("menuitem", {name: "Stop"}));
  expect(onPlan).toHaveBeenCalledTimes(1);
});
it("node hover cards retain ownership, dependency, resource and port diagnostics", async () => {
  const session = fixture();
  render(<QueryClientProvider client={new QueryClient()}><MemoryRouter><WorkspaceDetail session={session} resources={[]} sessions={[session]} now={Date.now()} connected onPlan={() => {}} tab="services" target="" onTabChange={() => {}} onTargetChange={() => {}} /></MemoryRouter></QueryClientProvider>);
  await userEvent.hover(screen.getByRole("button", {name: "Node details: backend/api"}));
  expect(await screen.findByText("process:123:456")).toBeVisible();
  expect(screen.getByText("db")).toBeVisible();
  await userEvent.unhover(screen.getByRole("button", {name: "Node details: backend/api"}));
  await userEvent.hover(screen.getByRole("button", {name: "Port details: backend/api"}));
  expect(await screen.findByText("Port 5102")).toBeVisible();
  expect(screen.getByText("owned")).toBeVisible();
});
it("port Select preserves declared-port review requests and supports keyboard selection", async () => {
  Element.prototype.scrollIntoView = vi.fn();
  Element.prototype.hasPointerCapture = vi.fn(() => false);
  const session = fixture();
  session.snapshot!.nodes = [{...node, allowed_actions: ["release"], ports: [{port: 5102, status: "external", reason: "Occupied", resource_refs: []}]}];
  const onPlan = vi.fn();
  render(<QueryClientProvider client={new QueryClient()}><MemoryRouter><WorkspaceDetail session={session} resources={[]} sessions={[session]} now={Date.now()} connected onPlan={onPlan} tab="services" target="" onTabChange={() => {}} onTargetChange={() => {}} /></MemoryRouter></QueryClientProvider>);
  await userEvent.click(screen.getByRole("checkbox", {name: "Select backend/api"}));
  const select = screen.getByRole("combobox", {name: "Port to resolve"});
  expect(screen.getByRole("button", {name: "Review port resolution"})).toBeDisabled();
  select.focus();
  await userEvent.keyboard("{ArrowDown}");
  expect(await screen.findByRole("option", {name: "5102 · Occupied"})).toBeVisible();
  await userEvent.keyboard("{End}{Enter}");
  expect(select).toHaveTextContent("5102 · Occupied");
  await userEvent.click(screen.getByRole("button", {name: "Review port resolution"}));
  expect(onPlan).toHaveBeenCalledWith([{session, action: "release", targets: ["backend/api"], port: 5102}]);
});
