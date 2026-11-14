import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, useLocation } from "react-router";
import { WorkspaceCatalogue } from "../desktop/WorkspaceCatalogue";
import { api, ApiFailure } from "../api/client";
import { desktop } from "../desktop/bridge";
import type { WorkspaceEntry } from "../api/types";
const offline: WorkspaceEntry = { root: "/projects/api", namespace: "personal", workspace: "stackharbor.workspace.yaml", state: "offline" };
beforeEach(() => { vi.spyOn(api, "workspaces").mockResolvedValue({ workspaces: [offline] }); });
afterEach(() => { delete window.__STACKHARBOR_DESKTOP__; });
function Location() { const route = useLocation(); return <p data-testid="route">{route.pathname}</p>; }
function mount() { return render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><MemoryRouter><WorkspaceCatalogue /><Location /></MemoryRouter></QueryClientProvider>); }

it("lists configured offline workspaces and refuses blind open for uncertain recovery", async () => {
  vi.mocked(api.workspaces).mockResolvedValue({ workspaces: [offline, { ...offline, root: "/projects/uncertain", state: "recovery_required", issues: ["Unclean engine exit"] }] });
  mount();
  expect(await screen.findByText(offline.root)).toBeVisible();
  const uncertain = screen.getByText("/projects/uncertain").closest("li")!;
  expect(within(uncertain).getByText("recovery_required")).toBeVisible();
  expect(within(uncertain).getByText("Unclean engine exit")).toBeVisible();
  expect(within(uncertain).getByRole("button", { name: "Open workspace" })).toBeDisabled();
  expect(screen.queryByRole("button", { name: "Choose folder…" })).not.toBeInTheDocument();
});

it("adds configuration only, without creating a session or starting services", async () => {
  const add = vi.spyOn(api, "addWorkspace").mockResolvedValue(offline);
  const open = vi.spyOn(api, "openWorkspace");
  const plan = vi.spyOn(api, "plan");
  mount();
  await userEvent.type(screen.getByRole("textbox", { name: "Workspace folder" }), offline.root);
  await userEvent.type(screen.getByRole("textbox", { name: "Configuration file (optional)" }), "custom.yaml");
  await userEvent.click(screen.getByRole("button", { name: "Add workspace" }));
  expect(add).toHaveBeenCalledExactlyOnceWith(offline.root, "custom.yaml");
  expect(open).not.toHaveBeenCalled();
  expect(plan).not.toHaveBeenCalled();
  expect(screen.getByRole("textbox", { name: "Workspace folder" })).toHaveValue("");
});

it("opens the explicitly selected workspace session without submitting a start operation", async () => {
  const open = vi.spyOn(api, "openWorkspace").mockResolvedValue({ root: offline.root, session_id: "opened-session", workspace_id: "ws", pid: 10, protocol_version: 1 });
  const submit = vi.spyOn(api, "submit");
  mount();
  await userEvent.click(await screen.findByRole("button", { name: "Open workspace" }));
  expect(open).toHaveBeenCalledExactlyOnceWith(offline.root, offline.workspace);
  expect(submit).not.toHaveBeenCalled();
  expect(screen.getByTestId("route")).toHaveTextContent("/workspaces/opened-session");
});

it("blocks a repeated unknown workspace mutation until actual state is refreshed", async () => {
  const open = vi.spyOn(api, "openWorkspace").mockRejectedValue(new ApiFailure(0, "outcome_unknown", "The workspace may already be open."));
  mount();
  await userEvent.click(await screen.findByRole("button", { name: "Open workspace" }));
  expect(await screen.findByText("Workspace action outcome unknown")).toBeVisible();
  expect(screen.getByRole("button", { name: "Open workspace" })).toBeDisabled();
  expect(open).toHaveBeenCalledTimes(1);
  vi.mocked(api.workspaces).mockResolvedValue({ workspaces: [{ ...offline, state: "live", session: { root: offline.root, session_id: "retained", workspace_id: "ws", pid: 10, protocol_version: 1 } }] });
  await userEvent.click(screen.getByRole("button", { name: "Refresh catalogue" }));
  expect(await screen.findByRole("link", { name: "View session" })).toHaveAttribute("href", "/workspaces/retained");
  expect(open).toHaveBeenCalledTimes(1);
  expect(screen.queryByText("Workspace action outcome unknown")).not.toBeInTheDocument();
});

it("uses the native folder picker only inside the new desktop bundle", async () => {
  window.__STACKHARBOR_DESKTOP__ = { namespace: "personal" };
  const pick = vi.spyOn(desktop, "pickWorkspace").mockResolvedValue("/picked/workspace");
  mount();
  await userEvent.click(screen.getByRole("button", { name: "Choose folder…" }));
  await waitFor(() => expect(screen.getByRole("textbox", { name: "Workspace folder" })).toHaveValue("/picked/workspace"));
  expect(pick).toHaveBeenCalledTimes(1);
});
