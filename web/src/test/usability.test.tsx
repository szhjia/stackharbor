import { render, screen, within, fireEvent, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { useState } from "react";
import { beforeEach, expect, it, vi } from "vitest";
import type { Node, Session, Operation } from "../api/types";
import { WorkspaceDetail } from "../pages/WorkspaceDetail";
import { Operations } from "../pages/Operations";
import { Resources } from "../pages/Resources";
import { LogView } from "../components/LogView";
import { PlanDialog } from "../components/PlanDialog";
import { api } from "../api/client";
import { PreferencesProvider } from "../lib/preferences";
import { App } from "../App";

const metric = {known: true, partial: false, sampled_at: null, rss_bytes: 1048576, cpu_percent: 2, uptime_millis: null};
beforeEach(() => {
  Element.prototype.hasPointerCapture = vi.fn(() => false);
  Element.prototype.scrollIntoView = vi.fn();
});
const identity = {session_id: "exact", workspace_id: "workspace", protocol_version: 1, capabilities: []};
const node: Node = {id: "backend/api", name: "API service", project_id: "backend", kind: "service", state: "running", reason: "", ownership: "session", allowed_actions: ["restart", "stop"], resource_refs: [], depends_on: [], metric, ports: []};
function session(): Session {
  return {identity, root: "/work/fixture", pid: 123, available: true, stale: false, last_seen: null, started_at: new Date().toISOString(), snapshot: {identity, revision: 1, observed_at: new Date().toISOString(), nodes: [node, {...node, id: "build", name: "Build", kind: "task", state: "failed", reason: "Build failed", allowed_actions: ["start"]}], diagnostics: [], processes: [], containers: []}};
}
function mountDetail(current = session()) {
  const onPlan = vi.fn();
  function Detail() {
    const [tab, setTab] = useState("services");
    return <WorkspaceDetail session={current} sessions={[current]} resources={[]} now={Date.now()} connected onPlan={onPlan} tab={tab} target="" onTabChange={setTab} onTargetChange={() => {}} />;
  }
  render(<QueryClientProvider client={new QueryClient()}><MemoryRouter><Detail /></MemoryRouter></QueryClientProvider>);
  return onPlan;
}
it("clears selection when leaving services and never operates on a hidden service from tasks", async () => {
  const onPlan = mountDetail();
  await userEvent.click(screen.getByRole("checkbox", {name: "Select backend/api"}));
  await userEvent.click(screen.getByRole("tab", {name: "Tasks"}));
  expect(screen.queryByText("1 nodes selected")).not.toBeInTheDocument();
  expect(screen.queryByRole("button", {name: "restart selected"})).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole("checkbox", {name: "Select build"}));
  await userEvent.click(screen.getByRole("button", {name: "start selected"}));
  expect(onPlan).toHaveBeenLastCalledWith([expect.objectContaining({action: "start", targets: ["build"]})]);
});
it("searches visible names, ports and localized state and exposes hidden selection with a clear action", async () => {
  const current = session();
  current.snapshot!.nodes.push({...node, id: "frontend/web", name: "Web client", state: "failed", ports: [{port: 5173, status: "external", reason: "Occupied", resource_refs: []}]});
  mountDetail(current);
  const filter = screen.getByRole("textbox", {name: "Filter services"});
  await userEvent.click(screen.getByRole("checkbox", {name: "Select backend/api"}));
  fireEvent.change(filter, {target: {value: "Web client"}});
  expect(screen.getByRole("button", {name: "Node details: frontend/web"})).toBeVisible();
  expect(screen.getByText("1 selected outside this filter")).toBeVisible();
  await userEvent.click(screen.getByRole("button", {name: "Clear selection"}));
  expect(screen.queryByText("1 nodes selected")).not.toBeInTheDocument();
  fireEvent.change(filter, {target: {value: "5173"}});
  expect(screen.getByRole("button", {name: "Node details: frontend/web"})).toBeVisible();
  await userEvent.click(screen.getByRole("button", {name: "Clear search"}));
  expect(filter).toHaveValue("");
});
it("keeps literal all and default search terms instead of treating them as filter defaults", () => {
  mountDetail();
  const filter = screen.getByRole("textbox", {name: "Filter services"});
  for (const value of ["all", "default"]) {
    fireEvent.change(filter, {target: {value}});
    expect(filter).toHaveValue(value);
  }
});
it("offers port conflict review next to the affected node without a selection prerequisite", async () => {
  const current = session();
  current.snapshot!.nodes = [{...node, state: "failed", reason: "Port is occupied", allowed_actions: ["start", "release"], ports: [{port: 5173, status: "external", reason: "Occupied", resource_refs: []}]}];
  const onPlan = mountDetail(current);
  expect(screen.getByText("Port is occupied")).toBeVisible();
  await userEvent.click(screen.getByRole("button", {name: "Review port 5173 for backend/api"}));
  expect(onPlan).toHaveBeenCalledWith([expect.objectContaining({action: "release", targets: ["backend/api"], port: 5173})]);
});
it("searches operation action and target, shows timestamps and links failures to exact logs", async () => {
  const current = session();
  const op: Operation = {id: "op1", session_id: "exact", action: "restart", state: "failed", created_at: "2026-10-15T01:00:00Z", updated_at: "2026-10-15T01:01:00Z", results: [{target: "frontend/web", state: "failed"}]};
  vi.spyOn(api, "operations").mockResolvedValue([op]);
  render(<QueryClientProvider client={new QueryClient()}><MemoryRouter><Operations sessions={[current]} tracked={[]} onError={() => {}} /></MemoryRouter></QueryClientProvider>);
  const filter = await screen.findByRole("textbox", {name: "Filter operations"});
  fireEvent.change(filter, {target: {value: "frontend/web"}});
  expect(screen.getByRole("link", {name: "Logs for frontend/web"})).toHaveAttribute("href", "/workspaces/exact/logs?target=frontend%2Fweb");
  fireEvent.change(filter, {target: {value: "restart"}});
  expect(screen.getByText("op1")).toBeVisible();
  expect(document.querySelector('time[datetime="2026-10-15T01:00:00Z"]')).toBeVisible();
});
it("recovers a bookmarked exact operation even after its session leaves discovery", async () => {
  const operation: Operation = {id: "ended-op", session_id: "ended-session", action: "close", state: "succeeded", created_at: "2026-10-15T01:00:00Z", updated_at: "2026-10-15T01:01:00Z", results: []};
  vi.spyOn(api, "operation").mockResolvedValue(operation);
  render(<QueryClientProvider client={new QueryClient()}><MemoryRouter initialEntries={["/operations?session=ended-session&operation=ended-op&q=ended-op"]}><Operations sessions={[]} tracked={[]} onError={() => {}} /></MemoryRouter></QueryClientProvider>);
  expect(await screen.findByRole("link", {name: "ended-op"})).toHaveAttribute("href", "/operations?session=ended-session&operation=ended-op&q=ended-op");
  expect(api.operation).toHaveBeenCalledWith("ended-session", "ended-op");
});
it("keeps accepted operation tracking across a tab reload after the session disappears", async () => {
  sessionStorage.clear();
  vi.stubGlobal("EventSource", class { addEventListener() {} close() {} });
  const current = session();
  const total = {count: 0, known_memory_count: 0, known_cpu_count: 0, memory_bytes: null, cpu_percent: null, partial: false, sampled_at: null};
  const inventory = {sessions: [current], resources: [], totals: {processes: total, containers: total}, partial: false, collected_at: new Date().toISOString(), event_cursor: "event:1"};
  const op: Operation = {id: "accepted-op", session_id: "exact", action: "restart", state: "succeeded", created_at: new Date().toISOString(), updated_at: new Date().toISOString(), results: []};
  vi.spyOn(api, "bootstrap").mockResolvedValue({csrf: "test", expires_at: "2099"});
  const inventoryRequest = vi.spyOn(api, "inventory").mockResolvedValue(inventory);
  vi.spyOn(api, "plan").mockResolvedValue({id: "p", session_id: "exact", action: "restart", targets: [node.id], affected: [node.id], warnings: [], fingerprint: "f", expires_at: "2099-01-01T00:00:00Z"});
  vi.spyOn(api, "submit").mockResolvedValue(op);
  vi.spyOn(api, "operations").mockResolvedValue([op]);
  vi.spyOn(api, "operation").mockResolvedValue(op);
  const mount = () => render(<QueryClientProvider client={new QueryClient()}><App /></QueryClientProvider>);
  history.replaceState(null, "", "/workspaces/exact");
  const first = mount();
  try {
    await userEvent.click(await screen.findByRole("button", {name: "Actions for backend/api"}));
    await userEvent.click(screen.getByRole("menuitem", {name: "Restart"}));
    await userEvent.click(await screen.findByRole("button", {name: "Confirm restart"}));
    expect(await screen.findByRole("link", {name: "accepted-op"})).toBeVisible();
    first.unmount();
    inventoryRequest.mockResolvedValue({...inventory, sessions: []});
    history.replaceState(null, "", "/operations");
    mount();
    expect(await screen.findByRole("link", {name: "accepted-op"})).toBeVisible();
  } finally { sessionStorage.clear(); vi.unstubAllGlobals(); }
});
it("searches translated service states", async () => {
  localStorage.setItem("stackharbor.language", "zh-CN");
  const current = session();
  render(<QueryClientProvider client={new QueryClient()}><MemoryRouter><PreferencesProvider><WorkspaceDetail session={current} sessions={[current]} resources={[]} now={Date.now()} connected onPlan={() => {}} tab="tasks" target="" onTabChange={() => {}} onTargetChange={() => {}} /></PreferencesProvider></MemoryRouter></QueryClientProvider>);
  try {
    fireEvent.change(screen.getByRole("textbox", {name: "筛选任务"}), {target: {value: "失败"}});
    expect(screen.getByRole("button", {name: "节点详情: build"})).toBeVisible();
  } finally { localStorage.clear(); }
});
it("opens a resource deep filter and supports numeric memory sorting with unknown values last", async () => {
  const resources = [1, 20, null].map((value, i) => ({id: `process:${i + 1}`, kind: "process", pid: i + 1, identity_known: true, metric: {...metric, rss_bytes: value}, references: []}));
  const view = render(<MemoryRouter initialEntries={["/resources?q=process%3A2"]}><Resources resources={resources} sessions={[]} /></MemoryRouter>);
  expect(screen.getAllByRole("row")).toHaveLength(2);
  expect(screen.getByRole("button", {name: "Resource details: process:2"})).toBeVisible();
  await userEvent.click(screen.getByRole("button", {name: "Clear search"}));
  await userEvent.click(screen.getByRole("combobox", {name: "Sort resources"}));
  await userEvent.click(screen.getByRole("option", {name: "Memory: highest first"}));
  const ids = screen.getAllByRole("row").slice(1).map(row => within(row).getByRole("button", {name: /Resource details/}).getAttribute("aria-label"));
  expect(ids).toEqual(["Resource details: process:2", "Resource details: process:1", "Resource details: process:3"]);
  view.unmount();
});
const logs = [
  {sequence: 1, time: "2026-10-15T01:00:00Z", project_id: "app", service_id: "api", stream: "stdout", text: "Listening"},
  {sequence: 2, time: "2026-10-15T01:00:01Z", project_id: "app", service_id: "web", stream: "stderr", text: "Port occupied"},
];
it.each(["stdout", "stderr"])("filters and copies %s from services and every task phase while preserving phase labels", async stream => {
  const copy = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, "clipboard", {value: {writeText: copy}, configurable: true});
  const streams = ["stdout", "stderr", "checking/stdout", "checking/stderr", "running-task/stdout", "running-task/stderr", "verifying/stdout", "verifying/stderr", "diagnostic", "stdout/diagnostic"];
  const entries = streams.map((value, index) => ({...logs[0], sequence: index + 1, service_id: "build", stream: value, text: `output ${value}`}));
  render(<LogView entries={entries} connected gap={0} />);
  await userEvent.click(screen.getByRole("combobox", {name: "Output stream"}));
  await userEvent.click(screen.getByRole("option", {name: stream}));
  const expected = entries.filter(entry => entry.stream === stream || entry.stream.endsWith(`/${stream}`));
  for (const entry of entries) {
    if (expected.includes(entry)) expect(screen.getByText(entry.text)).toBeVisible();
    else expect(screen.queryByText(entry.text)).not.toBeInTheDocument();
  }
  await userEvent.click(screen.getByRole("button", {name: "Copy visible logs"}));
  expect(copy).toHaveBeenLastCalledWith(expected.map(entry => `${entry.time} ${entry.service_id} / ${entry.stream} ${entry.text}`).join("\n"));
  fireEvent.change(screen.getByRole("textbox", {name: "Search logs"}), {target: {value: "verifying"}});
  expect(screen.getByText(`output verifying/${stream}`)).toBeVisible();
  expect(screen.queryByText(`output checking/${stream}`)).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", {name: "Copy visible logs"}));
  expect(copy).toHaveBeenLastCalledWith(expect.stringContaining(`build / verifying/${stream} output verifying/${stream}`));
  expect(copy.mock.lastCall![0]).not.toContain("checking/");
  fireEvent.change(screen.getByRole("textbox", {name: "Search logs"}), {target: {value: ""}});
  await userEvent.click(screen.getByRole("combobox", {name: "Output stream"}));
  await userEvent.click(screen.getByRole("option", {name: "All streams"}));
  for (const entry of entries) expect(screen.getByText(entry.text)).toBeVisible();
});
it("filters logs, copies visible output and reports new entries while paused", async () => {
  const copy = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, "clipboard", {value: {writeText: copy}, configurable: true});
  const props = {entries: logs, connected: true, gap: 0};
  const view = render(<LogView {...props} />);
  fireEvent.change(screen.getByRole("textbox", {name: "Search logs"}), {target: {value: "occupied"}});
  expect(screen.queryByText("Listening")).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", {name: "Copy visible logs"}));
  expect(copy).toHaveBeenCalledWith(expect.stringContaining("Port occupied"));
  expect(copy.mock.calls[0][0]).not.toContain("Listening");
  await userEvent.click(screen.getByRole("button", {name: "Pause following"}));
  view.rerender(<LogView {...props} entries={[...logs, {...logs[1], sequence: 3, text: "New failure"}]} />);
  expect(screen.getByRole("button", {name: "1 new entries · Jump to latest"})).toBeVisible();
});
it("pauses log following when scrolling up and resumes at the latest output", async () => {
  render(<LogView entries={logs} connected gap={0} />);
  const output = screen.getByLabelText("Session log output");
  Object.defineProperties(output, {scrollHeight: {value: 900, configurable: true}, clientHeight: {value: 200, configurable: true}});
  fireEvent.wheel(output, {deltaY: -100});
  fireEvent.scroll(output, {target: {scrollTop: 0}});
  expect(screen.getByRole("button", {name: "Resume following"})).toBeVisible();
  await userEvent.click(screen.getByRole("button", {name: "Jump to latest"}));
  expect(screen.getByRole("button", {name: "Pause following"})).toBeVisible();
});
it("separates requested targets from additional affected nodes in plan review", () => {
  render(<PlanDialog plans={[{root: "/work", plan: {id: "p", session_id: "s", action: "restart", targets: ["api"], affected: ["api", "web"], warnings: [], fingerprint: "f", expires_at: "2099-01-01T00:00:00Z"}}]} onSubmit={async () => {}} onClose={() => {}} />);
  expect(screen.getAllByText("api")).toHaveLength(1);
  expect(screen.getByText("Additional affected nodes · 1")).toBeVisible();
  expect(screen.getByText("web")).toBeVisible();
});
it("keeps the exact requested port visible when reviewing a direct port action", () => {
  render(<PlanDialog plans={[{root: "/work", port: 5173, plan: {id: "p", session_id: "s", action: "release", targets: ["web"], affected: ["web"], warnings: [], fingerprint: "f", expires_at: "2099-01-01T00:00:00Z"}}]} onSubmit={async () => {}} onClose={() => {}} />);
  expect(screen.getByText("Port 5173")).toBeVisible();
});
