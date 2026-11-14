import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App } from "../App";
import { api } from "../api/client";
import { desktop, type DesktopAgreement, type SetupStatus } from "../desktop/bridge";
import { DesktopGate, DesktopSettings, SetupPreview } from "../desktop/Setup";

const agreement: DesktopAgreement = { version: "1", hash: "signed-agreement-hash", publisher: "Publisher", text: "Full agreement text.\nCLI and skill are mandatory.\n# managed PATH preview\nexport PATH=\"$HOME/.local/bin:$PATH\"" };
const initial: SetupStatus = { complete: false, version: "1", bundle_id: "com.xahacktech.stackharbor", installed_path: "/Applications/StackHarbor.app", cli_path: "/home/user/.local/bin/stackharbor", skill_path: "/home/user/.agents/skills/stackharbor", path_configured: false, agreement: { version: "1", hash: agreement.hash, accepted: false }, autostart: { enabled: false, status: "off" }, background: { status: "enabled" }, conflicts: [], issues: [] };
const completed: SetupStatus = { ...initial, complete: true, agreement: { ...initial.agreement, accepted: true } };
beforeEach(() => {
  window.__STACKHARBOR_DESKTOP__ = { namespace: "test" };
  vi.spyOn(desktop, "status").mockResolvedValue(initial);
  vi.spyOn(desktop, "agreement").mockResolvedValue(agreement);
});
afterEach(() => { delete window.__STACKHARBOR_DESKTOP__; });
async function acknowledge() {
  await userEvent.click(await screen.findByRole("checkbox", { name: "I accept this version of the installation agreement." }));
  await userEvent.click(screen.getByRole("checkbox", { name: /Install the required CLI/ }));
  await userEvent.click(screen.getByRole("checkbox", { name: /Install the required skill/ }));
}

it("keeps the console gated until real setup verifies required integrations", async () => {
  const setup = vi.spyOn(desktop, "setup").mockResolvedValueOnce({ ...initial, issues: ["Skill verification failed"] }).mockResolvedValueOnce(completed);
  render(<DesktopGate><p>Verified console</p></DesktopGate>);
  expect(screen.queryByText("Verified console")).not.toBeInTheDocument();
  expect(await screen.findByLabelText("Full installation agreement")).toHaveTextContent("Full agreement text.");
  expect(screen.getByLabelText("Full installation agreement").textContent).toBe(agreement.text);
  expect(screen.getByRole("button", { name: "Install and verify integrations" })).toBeDisabled();
  expect(screen.getByRole("checkbox", { name: /Start StackHarbor at login/ })).not.toBeChecked();
  await acknowledge();
  await userEvent.click(screen.getByRole("button", { name: "Install and verify integrations" }));
  expect(setup).toHaveBeenCalledWith({ acceptAgreement: true, agreementHash: agreement.hash, autoStart: false, migrateConflicts: false, configurePath: false });
  expect(await screen.findByText("Skill verification failed")).toBeVisible();
  expect(screen.queryByText("Verified console")).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Install and verify integrations" }));
  expect(await screen.findByText("Verified console")).toBeVisible();
});

it("shows actual approval, disabled background and ownership conflicts with an explicit migration choice", async () => {
  vi.mocked(desktop.status).mockResolvedValue({ ...initial, background: { status: "requiresApproval" }, conflicts: [{ path: initial.cli_path, reason: "User-owned executable" }] });
  const setup = vi.spyOn(desktop, "setup").mockResolvedValue(initial);
  render(<DesktopGate>Console</DesktopGate>);
  expect(await screen.findByText("Approval required")).toBeVisible();
  expect(screen.getByText(/Allow StackHarbor in System Settings/)).toBeVisible();
  expect(screen.getByText(/User-owned executable/)).toBeVisible();
  const migration = await screen.findByRole("checkbox", { name: /Back up and migrate/ });
  expect(migration).not.toBeChecked();
  await acknowledge();
  await userEvent.click(migration);
  await userEvent.click(screen.getByRole("checkbox", { name: /Add the reversible managed PATH block/ }));
  await userEvent.click(screen.getByRole("button", { name: "Install and verify integrations" }));
  expect(setup).toHaveBeenCalledWith(expect.objectContaining({ migrateConflicts: true, configurePath: true, autoStart: false }));
});

it("does not claim successful installation after a lost native response and allows status inspection", async () => {
  const setup = vi.spyOn(desktop, "setup").mockRejectedValue(new Error("Native connection lost"));
  render(<DesktopGate>Console</DesktopGate>);
  await acknowledge();
  await userEvent.click(screen.getByRole("button", { name: "Install and verify integrations" }));
  expect(await screen.findByText(/Native connection lost/)).toBeVisible();
  expect(screen.getByRole("button", { name: "Install and verify integrations" })).toBeDisabled();
  expect(screen.queryByText("Console")).not.toBeInTheDocument();
  expect(setup).toHaveBeenCalledTimes(1);
  vi.mocked(desktop.status).mockResolvedValue(completed);
  await userEvent.click(screen.getByRole("button", { name: "Check actual status" }));
  expect(await screen.findByText("Console")).toBeVisible();
});

it("shows a failed status refresh and retains the unknown mutation guard", async () => {
  vi.spyOn(desktop, "setup").mockRejectedValue(new Error("Setup reply lost"));
  render(<DesktopGate>Console</DesktopGate>);
  await acknowledge();
  await userEvent.click(screen.getByRole("button", { name: "Install and verify integrations" }));
  await screen.findByText(/Setup reply lost/);
  vi.mocked(desktop.status).mockRejectedValue(new Error("Engine status unreachable"));
  await userEvent.click(screen.getByRole("button", { name: "Check actual status" }));
  expect(await screen.findByText("Status could not be refreshed")).toBeVisible();
  expect(screen.getByText("Engine status unreachable")).toBeVisible();
  expect(screen.getByRole("button", { name: "Install and verify integrations" })).toBeDisabled();
});

it("never opens native setup or system commands in an ordinary browser", () => {
  delete window.__STACKHARBOR_DESKTOP__;
  render(<DesktopGate><p>Browser console</p></DesktopGate>);
  expect(screen.getByText("Browser console")).toBeVisible();
  expect(desktop.status).not.toHaveBeenCalled();
});

it("offers explicit preview states without installing or simulating success", async () => {
  delete window.__STACKHARBOR_DESKTOP__;
  const setup = vi.spyOn(desktop, "setup");
  render(<MemoryRouter initialEntries={["/desktop/setup-preview?state=disabled"]}><SetupPreview /></MemoryRouter>);
  expect(await screen.findByText("Browser preview")).toBeVisible();
  expect(screen.getByText("Background engine unavailable")).toBeVisible();
  await acknowledge();
  expect(screen.getByRole("button", { name: "Install and verify integrations" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "Check actual status" })).toBeDisabled();
  expect(desktop.status).not.toHaveBeenCalled();
  expect(desktop.agreement).not.toHaveBeenCalled();
  expect(setup).not.toHaveBeenCalled();
});

it("leaves the explicit preview route when browser history restores a console route", async () => {
  delete window.__STACKHARBOR_DESKTOP__;
  history.replaceState(null, "", "/desktop/setup-preview");
  vi.spyOn(api, "bootstrap").mockResolvedValue({ csrf: "test", expires_at: "later" });
  vi.spyOn(api, "inventory").mockImplementation(() => new Promise(() => {}));
  render(<QueryClientProvider client={new QueryClient()}><App /></QueryClientProvider>);
  expect(await screen.findByText("Browser preview")).toBeVisible();
  act(() => { history.pushState(null, "", "/settings"); window.dispatchEvent(new PopStateEvent("popstate")); });
  expect(await screen.findByRole("combobox", { name: "Appearance" })).toBeVisible();
  expect(screen.queryByText("Browser preview")).not.toBeInTheDocument();
});

it("requires explicit uninstall confirmation and displays a running-project refusal without stopping anything", async () => {
  vi.mocked(desktop.status).mockResolvedValue(completed);
  const uninstall = vi.spyOn(desktop, "uninstall").mockRejectedValue(new Error("Stop running projects explicitly first"));
  const autostart = vi.spyOn(desktop, "autostart").mockResolvedValue({ ...completed, autostart: { enabled: true, status: "enabled" } });
  render(<DesktopGate><DesktopSettings /></DesktopGate>);
  await userEvent.click(await screen.findByRole("button", { name: "Enable login startup" }));
  expect(autostart).toHaveBeenCalledWith(true);
  expect(await screen.findByRole("button", { name: "Disable login startup" })).toBeVisible();
  await userEvent.click(screen.getByRole("button", { name: "Uninstall integrations…" }));
  const dialog = screen.getByRole("dialog", { name: "Remove StackHarbor integrations?" });
  expect(uninstall).not.toHaveBeenCalled();
  expect(within(dialog).getByText(/does not stop projects/)).toBeVisible();
  await userEvent.click(within(dialog).getByRole("button", { name: "Remove owned integrations" }));
  await waitFor(() => expect(uninstall).toHaveBeenCalledTimes(1));
  expect(await screen.findByText(/Stop running projects explicitly first/)).toBeVisible();
  expect(screen.getByRole("dialog", { name: "Remove StackHarbor integrations?" })).toBeVisible();
});
