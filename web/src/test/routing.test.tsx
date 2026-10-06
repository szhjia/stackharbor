import userEvent from "@testing-library/user-event";
import { beforeEach, it, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App } from "../App";
import { api } from "../api/client";
import type { Inventory, Session } from "../api/types";

const identity = { session_id: "exact", workspace_id: "workspace", protocol_version: 1, capabilities: [] };
const fixture: Session = {
  identity, root: "/owned/fixture", pid: 123, available: true, stale: false,
  started_at: new Date().toISOString(), last_seen: new Date().toISOString(),
  snapshot: { identity, revision: 1, observed_at: new Date().toISOString(), nodes: [], processes: [], containers: [], diagnostics: [] },
};
const total = { count: 0, known_memory_count: 0, known_cpu_count: 0, memory_bytes: null, cpu_percent: null, partial: false, sampled_at: null };
beforeEach(() => {
  localStorage.clear();
  history.replaceState(null, "", "/");
  vi.stubGlobal("EventSource", class { addEventListener() {} close() {} });
  vi.spyOn(api, "bootstrap").mockResolvedValue({ csrf: "c", expires_at: "2099" });
  vi.spyOn(api, "inventory").mockResolvedValue({ sessions: [fixture], resources: [], totals: { processes: total, containers: total }, partial: false, collected_at: new Date().toISOString(), event_cursor: "event:1" } satisfies Inventory);
  vi.spyOn(api, "logs").mockResolvedValue({ session_id: "exact", target: "app/api", entries: [], gap: false, dropped: 0, reset: false, next_cursor: 0, cursor: "0" });
});
it("sidebar toggle preserves navigation and remembers the layout after remount", async () => {
  const view = mount("/");
  const collapse = await screen.findByRole("button", {name: "Collapse sidebar"});
  expect(collapse).toHaveAttribute("aria-expanded", "true");
  await userEvent.click(collapse);
  expect(screen.getByRole("button", {name: "Expand sidebar"})).toHaveAttribute("aria-expanded", "false");
  expect(document.querySelector(".console")).toHaveClass("sidebar-collapsed");
  await userEvent.click(screen.getByRole("link", {name: "Resources"}));
  expect(await screen.findByRole("heading", {name: "Resources"})).toBeVisible();
  expect(screen.getByRole("link", {name: "Resources"})).toHaveAttribute("title", "Resources");
  expect(screen.getByRole("button", {name: "Expand sidebar"})).toBeVisible();
  view.unmount();
  mount("/resources");
  await userEvent.click(await screen.findByRole("button", {name: "Expand sidebar"}));
  expect(document.querySelector(".console")).not.toHaveClass("sidebar-collapsed");
  expect(localStorage.getItem("stackharbor.sidebar-collapsed")).toBe("false");
});
function mount(path: string) {
  history.replaceState(null, "", path);
  return render(<QueryClientProvider client={new QueryClient({defaultOptions: {queries: {retry: false}}})}><App /></QueryClientProvider>);
}
it("overview summarizes unique workspaces, session health and shared resources without duplicating lists", async () => {
  const metric = {known: false, partial: true, sampled_at: null, rss_bytes: null, cpu_percent: null, uptime_millis: null};
  const ref = {session_id: "exact", workspace_id: "workspace", node_ids: ["api"], nodes: []};
  vi.mocked(api.inventory).mockResolvedValue({
    sessions: [fixture, {...fixture, identity: {...identity, session_id: "stale"}, stale: true}, {...fixture, identity: {...identity, session_id: "offline", workspace_id: "other"}, available: false}],
    resources: [
      {id: "process:1", kind: "process", identity_known: true, metric, references: [ref, ref, {...ref, node_ids: ["web"]}]},
      {id: "container:1", kind: "container", identity_known: false, metric, references: [ref, ref]},
    ],
    totals: {processes: total, containers: total}, partial: true, collected_at: new Date().toISOString(), event_cursor: "event:1",
  });
  mount("/");
  const workspaces = await screen.findByRole("region", {name: "Workspaces"});
  expect(within(workspaces).getByText("workspaces · 3 sessions").previousElementSibling).toHaveTextContent("2");
  expect([...workspaces.querySelectorAll("dd")].map((e) => e.textContent)).toEqual(["1", "1", "1"]);
  const resources = screen.getByRole("region", {name: "Resources"});
  expect([...resources.querySelectorAll("dd")].map((e) => e.textContent)).toEqual(["1", "1", "1", "1"]);
  expect(within(workspaces).getByRole("link", {name: "View all →"})).toHaveAttribute("href", "/workspaces");
  expect(within(resources).getByRole("link", {name: "View all →"})).toHaveAttribute("href", "/resources");
  expect(screen.queryByRole("table")).not.toBeInTheDocument();
  expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
});
it("direct menu URLs render their page and expose native navigation links", async () => {
  mount("/resources");
  expect(await screen.findByRole("heading", {name: "Resources"})).toBeVisible();
  expect(screen.getByRole("link", {name: "Resources"})).toHaveAttribute("aria-current", "page");
  fireEvent.click(screen.getByRole("link", {name: "Workspaces"}));
  expect(location.pathname).toBe("/workspaces");
  expect(await screen.findByRole("heading", {name: "Workspaces"})).toBeVisible();
  expect(screen.getByRole("link", {name: "/owned/fixture"})).toHaveAttribute("href", "/workspaces/exact");
  expect(screen.getAllByRole("columnheader").map((column) => column.textContent)).toEqual(["Select", "Workspace", "Session", "PID", "Terminal", "Connection", "Nodes", "Last observation"]);
  expect(api.bootstrap).toHaveBeenCalledTimes(1);
  expect(screen.getAllByRole("button", {name: "Refresh"})).toHaveLength(1);
});
it("workspace tabs and log target survive remount from a deep URL", async () => {
  const view = mount("/workspaces/exact/logs?target=app%2Fapi");
  expect(await screen.findByRole("heading", {name: "Logs", level: 1})).toBeVisible();
  expect(await screen.findByRole("link", {name: "fixture"})).toHaveAttribute("href", "/workspaces/exact");
  expect(screen.getByRole("tab", {name: "Logs"})).toHaveAttribute("aria-selected", "true");
  expect(screen.getByRole("combobox", {name: "Log target"})).toHaveTextContent("app/api");
  await userEvent.click(screen.getByRole("tab", {name: "Tasks"}));
  expect(location.pathname).toBe("/workspaces/exact/tasks");
  expect(location.search).toBe("");
  view.unmount();
  mount(location.pathname);
  expect(await screen.findByRole("tab", {name: "Tasks"})).toHaveAttribute("aria-selected", "true");
});
it("browser back and forward restore the matching menu", async () => {
  mount("/");
  fireEvent.click(await screen.findByRole("link", {name: "Workspaces"}));
  fireEvent.click(screen.getByRole("link", {name: "Resources"}));
  history.back();
  await waitFor(() => expect(screen.getByRole("heading", {name: "Workspaces"})).toBeVisible());
  history.forward();
  await waitFor(() => expect(screen.getByRole("heading", {name: "Resources"})).toBeVisible());
});
it("unknown routes and ended sessions never silently render another page", async () => {
  const view = mount("/not-a-page");
  expect(await screen.findByRole("heading", {name: "Page not found"})).toBeVisible();
  view.unmount();
  mount("/workspaces/ended/logs");
  expect(await screen.findByText("This session ended or disappeared")).toBeVisible();
  expect(screen.getByRole("link", {name: "All workspaces"})).toHaveAttribute("href", "/workspaces");
});
