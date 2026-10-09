import userEvent from "@testing-library/user-event";
import { beforeEach, it, expect, vi } from "vitest";
import { act, render, screen, fireEvent, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App } from "../App";
import { api } from "../api/client";
import { initializePreferences } from "../lib/preferences";
import type { Inventory, Session } from "../api/types";

const identity = { session_id: "exact", workspace_id: "workspace", protocol_version: 1, capabilities: [] };
const fixture: Session = {
  identity, root: "/owned/fixture", pid: 123, available: true, stale: false,
  started_at: new Date().toISOString(), last_seen: new Date().toISOString(),
  snapshot: { identity, revision: 1, observed_at: new Date().toISOString(), nodes: [], processes: [], containers: [], diagnostics: [] },
};
const total = { count: 0, known_memory_count: 0, known_cpu_count: 0, memory_bytes: null, cpu_percent: null, partial: false, sampled_at: null };
beforeEach(() => {
  Element.prototype.scrollIntoView = vi.fn();
  Element.prototype.hasPointerCapture = vi.fn(() => false);
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
  await userEvent.click(screen.getByRole("link", {name: "Runtime resources"}));
  expect(await screen.findByRole("heading", {name: "Runtime resources"})).toBeVisible();
  expect(screen.getByRole("link", {name: "Runtime resources"})).toHaveAttribute("title", "Runtime resources");
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
  expect(await screen.findByRole("heading", {name: "Workbench", level: 1})).toBeVisible();
  const health = await screen.findByRole("region", {name: "Session health"});
  expect([...health.querySelectorAll("dd")].map(e => e.textContent)).toEqual(["1", "1", "1"]);
  const attention = screen.getByRole("region", {name: "Needs your attention"});
  expect(within(attention).getAllByRole("link").map(e => e.getAttribute("href"))).toEqual(["/workspaces/stale", "/workspaces/offline", "/workspaces"]);
  expect(screen.getByText("No memory samples yet")).toBeVisible();
  expect(screen.queryByText("Shared resources")).not.toBeInTheDocument();
  expect(screen.queryByText("Unknown identity")).not.toBeInTheDocument();
  expect(screen.getAllByRole("link", {name: "Operations"})).toHaveLength(1);
  expect(screen.getByText("Partial observation")).toBeVisible();
  expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
});
it("direct menu URLs render their page and expose native navigation links", async () => {
  mount("/resources");
  expect(await screen.findByRole("heading", {name: "Runtime resources"})).toBeVisible();
  expect(screen.getByRole("link", {name: "Runtime resources"})).toHaveAttribute("aria-current", "page");
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
  fireEvent.click(screen.getByRole("link", {name: "Runtime resources"}));
  history.back();
  await waitFor(() => expect(screen.getByRole("heading", {name: "Workspaces"})).toBeVisible());
  history.forward();
  await waitFor(() => expect(screen.getByRole("heading", {name: "Runtime resources"})).toBeVisible());
});
it("unknown routes and ended sessions never silently render another page", async () => {
  const view = mount("/not-a-page");
  expect(await screen.findByRole("heading", {name: "Page not found"})).toBeVisible();
  view.unmount();
  mount("/workspaces/ended/logs");
  expect(await screen.findByText("This session ended or disappeared")).toBeVisible();
  expect(screen.getByRole("link", {name: "All workspaces"})).toHaveAttribute("href", "/workspaces");
});

async function choosePreference(name: string, value: string) {
  await userEvent.click(await screen.findByRole("combobox", {name}));
  const option = value === "zh-CN" ? "中文" : value === "en" ? "English"
    : value === "system" ? /^(Follow system|跟随系统)$/
    : value === "dark" ? /^(Dark|深色)$/ : value === "light" ? /^(Light|浅色)$/
    : value === "sm" ? /^(Small \(12px\)|小（12px）)$/
    : value === "md" ? /^(Medium \(13px\)|中（13px）)$/ : /^(Large \(14px\)|大（14px）)$/;
  await userEvent.click(screen.getByRole("option", {name: option}));
}
it("settings lives in the sidebar footer, works collapsed and persists preferences", async () => {
  const view = mount("/");
  await userEvent.click(await screen.findByRole("button", {name: "Collapse sidebar"}));
  const settings = screen.getByRole("link", {name: "Settings"});
  expect(settings.closest("footer")).toHaveClass("sidebar-footer");
  expect(settings).toHaveAttribute("title", "Settings");
  await userEvent.click(settings);
  expect(location.pathname).toBe("/settings");
  expect(settings).toHaveAttribute("aria-current", "page");
  await choosePreference("Interface language", "zh-CN");
  expect(screen.getByRole("heading", {name: "设置", level: 1})).toBeVisible();
  expect(screen.getByRole("link", {name: "工作区"})).toBeVisible();
  expect(document.documentElement.lang).toBe("zh-CN");
  expect(document.title).toBe("设置 · StackHarbor");
  await choosePreference("外观", "dark");
  expect(document.documentElement).toHaveClass("dark");
  expect(document.documentElement.style.colorScheme).toBe("dark");
  expect(screen.getByRole("combobox", {name: "文字大小"})).toHaveTextContent("小（12px）");
  expect(document.documentElement).toHaveAttribute("data-font-size", "sm");
  await choosePreference("文字大小", "lg");
  expect(document.documentElement).toHaveAttribute("data-font-size", "lg");
  view.unmount();
  mount("/settings");
  expect(await screen.findByRole("combobox", {name: "界面语言"})).toHaveTextContent("中文");
  expect(screen.getByRole("combobox", {name: "外观"})).toHaveTextContent("深色");
  expect(screen.getByRole("combobox", {name: "文字大小"})).toHaveTextContent("大（14px）");
  expect(localStorage.getItem("stackharbor.language")).toBe("zh-CN");
  expect(localStorage.getItem("stackharbor.theme")).toBe("dark");
  expect(localStorage.getItem("stackharbor.font-size")).toBe("lg");
  await choosePreference("界面语言", "en");
  await choosePreference("Appearance", "light");
  await choosePreference("Text size", "md");
  expect(document.documentElement).toHaveAttribute("data-font-size", "md");
  expect(document.documentElement).not.toHaveClass("dark");
  expect(document.documentElement.lang).toBe("en");
});
it("Chinese selection updates destination tables while workspace identities stay unchanged", async () => {
  mount("/settings");
  await choosePreference("Interface language", "zh-CN");
  await userEvent.click(screen.getByRole("link", {name: "工作区"}));
  expect(await screen.findByRole("columnheader", {name: "最近观测"})).toBeVisible();
  expect(screen.getByRole("link", {name: "/owned/fixture"})).toHaveAttribute("href", "/workspaces/exact");
  expect(screen.getByRole("textbox", {name: "筛选工作区"})).toHaveAttribute("placeholder", "查找工作区…");
});
it("settings can be opened directly before the inventory is available", async () => {
  vi.mocked(api.inventory).mockImplementation(() => new Promise(() => {}));
  mount("/settings");
  expect(await screen.findByRole("combobox", {name: "Appearance"})).toBeVisible();
  expect(screen.queryByText("Waiting for the first successful inventory refresh…")).not.toBeInTheDocument();
});
it("follows system appearance from first paint and updates only while selected", async () => {
  let dark = true;
  const listeners = new Set<() => void>();
  const media = {
    get matches() { return dark; },
    addEventListener: vi.fn((_event: string, listener: () => void) => listeners.add(listener)),
    removeEventListener: vi.fn((_event: string, listener: () => void) => listeners.delete(listener)),
  } as unknown as MediaQueryList;
  vi.stubGlobal("matchMedia", vi.fn(() => media));
  try {
    initializePreferences();
    expect(document.documentElement).toHaveClass("dark");
    expect(document.documentElement.style.colorScheme).toBe("dark");
    const view = mount("/settings");
    expect(await screen.findByRole("combobox", {name: "Appearance"})).toHaveTextContent("Follow system");
    expect(localStorage.getItem("stackharbor.theme")).toBeNull();
    act(() => { dark = false; listeners.forEach(listener => listener()); });
    expect(document.documentElement).not.toHaveClass("dark");
    expect(document.documentElement.style.colorScheme).toBe("light");
    await choosePreference("Appearance", "light");
    act(() => { dark = true; listeners.forEach(listener => listener()); });
    expect(document.documentElement).not.toHaveClass("dark");
    await choosePreference("Appearance", "system");
    expect(document.documentElement).toHaveClass("dark");
    expect(localStorage.getItem("stackharbor.theme")).toBe("system");
    view.unmount();
    expect(listeners.size).toBe(0);
    mount("/settings");
    expect(await screen.findByRole("combobox", {name: "Appearance"})).toHaveTextContent("Follow system");
  } finally {
    vi.unstubAllGlobals();
  }
});
it("invalid stored preferences fall back safely and unavailable storage does not block switching", async () => {
  localStorage.setItem("stackharbor.language", "invalid");
  localStorage.setItem("stackharbor.theme", "invalid");
  localStorage.setItem("stackharbor.font-size", "invalid");
  mount("/settings");
  expect(await screen.findByRole("combobox", {name: "Interface language"})).toHaveTextContent("English");
  expect(screen.getByRole("combobox", {name: "Appearance"})).toHaveTextContent("Follow system");
  expect(screen.getByRole("combobox", {name: "Text size"})).toHaveTextContent("Small (12px)");
  vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("Storage unavailable"); });
  await choosePreference("Interface language", "zh-CN");
  await choosePreference("外观", "dark");
  await choosePreference("文字大小", "md");
  expect(document.documentElement).toHaveClass("dark");
  expect(document.documentElement).toHaveAttribute("data-font-size", "md");
  expect(screen.getByRole("heading", {name: "设置", level: 1})).toBeVisible();
});
