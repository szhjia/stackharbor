import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App } from "../App";
import { api } from "../api/client";
import { desktop, type SetupStatus } from "../desktop/bridge";
import type { Inventory } from "../api/types";

const total = { count: 0, known_memory_count: 0, known_cpu_count: 0, memory_bytes: null, cpu_percent: null, partial: false, sampled_at: null };
const inventory: Inventory = { sessions: [], resources: [], totals: { processes: total, containers: total }, partial: false, collected_at: new Date().toISOString(), event_cursor: "event:1" };
const installed: SetupStatus = {
  complete: true, version: "1", bundle_id: "com.xahacktech.stackharbor", installed_path: "/Applications/StackHarbor.app",
  cli_path: "/home/user/.local/bin/stackharbor", skill_path: "/home/user/.agents/skills/stackharbor", path_configured: false,
  agreement: { version: "1", hash: "agreement", accepted: true }, autostart: { enabled: false, status: "off" }, background: { status: "enabled" }, conflicts: [], issues: [],
};
beforeEach(() => {
  localStorage.clear();
  history.replaceState(null, "", "/workspaces");
  vi.stubGlobal("EventSource", class { addEventListener() {} close() {} });
  vi.spyOn(api, "bootstrap").mockResolvedValue({ csrf: "browser", expires_at: "later" });
  vi.spyOn(api, "inventory").mockResolvedValue(inventory);
  vi.spyOn(api, "workspaces").mockResolvedValue({ workspaces: [{ root: "/configured/offline", namespace: "personal", workspace: "stackharbor.workspace.yaml", state: "offline" }] });
});
afterEach(() => { delete window.__STACKHARBOR_DESKTOP__; vi.unstubAllGlobals(); });
function mount() {
  return render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><App /></QueryClientProvider>);
}

it.each([undefined, []] as (string[] | undefined)[])("preserves a legacy browser workspace page without catalogue requests or unsupported actions (capabilities: %s)", async capabilities => {
  vi.mocked(api.inventory).mockResolvedValue({ ...inventory, capabilities });
  vi.mocked(api.workspaces).mockRejectedValue(new Error("Legacy catalogue route is unsupported"));
  mount();
  expect(await screen.findByRole("table", { name: "workspaces" })).toBeVisible();
  expect(screen.queryByRole("button", { name: "Add workspace" })).not.toBeInTheDocument();
  expect(screen.queryByRole("heading", { name: "Configured workspaces" })).not.toBeInTheDocument();
  expect(screen.queryByText("Workspace catalogue unavailable")).not.toBeInTheDocument();
  expect(screen.queryByText("Legacy catalogue route is unsupported")).not.toBeInTheDocument();
  expect(api.workspaces).not.toHaveBeenCalled();
});

it("enables catalogue add/open only when a managed browser gateway advertises support", async () => {
  vi.mocked(api.inventory).mockResolvedValue({ ...inventory, capabilities: ["workspace_catalogue"] });
  mount();
  expect(await screen.findByText("/configured/offline")).toBeVisible();
  expect(screen.getByRole("button", { name: "Add workspace" })).toBeVisible();
  expect(screen.getByRole("button", { name: "Open workspace" })).toBeEnabled();
  expect(screen.queryByRole("button", { name: "Choose folder…" })).not.toBeInTheDocument();
  expect(api.workspaces).toHaveBeenCalledTimes(1);
});

it("retains the native catalogue and folder picker independently of browser capabilities", async () => {
  window.__STACKHARBOR_DESKTOP__ = { namespace: "personal" };
  vi.spyOn(desktop, "status").mockResolvedValue(installed);
  mount();
  expect(await screen.findByText("/configured/offline")).toBeVisible();
  expect(screen.getByRole("button", { name: "Add workspace" })).toBeVisible();
  expect(screen.getByRole("button", { name: "Choose folder…" })).toBeVisible();
  expect(api.workspaces).toHaveBeenCalledTimes(1);
});
