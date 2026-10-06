import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it } from "vitest";
import { WorkspaceInfo } from "../components/WorkspaceInfo";
import type { Session } from "../api/types";
import { MemoryRouter } from "react-router";
import { Workspaces } from "../pages/Workspaces";

const session: Session = {
  identity: {session_id: "full-session-identifier", workspace_id: "workspace", protocol_version: 1, capabilities: []},
  root: "/projects/workspace", pid: 1234, tty: "/dev/ttys005", available: true, stale: false,
  started_at: "2026-10-06T10:00:00Z", last_seen: "2026-10-06T11:00:00Z",
};
it("terminal shows its useful identifier on one trigger and reveals supporting details on hover", async () => {
  render(<WorkspaceInfo session={session} kind="terminal" />);
  const trigger = screen.getByRole("button", {name: "Terminal details: /dev/ttys005"});
  expect(trigger).toHaveTextContent("/dev/ttys005");
  expect(screen.queryByText("Foreground session")).not.toBeInTheDocument();
  await userEvent.hover(trigger);
  expect(await screen.findByText("Foreground session")).toBeVisible();
  expect(screen.getByText("1234")).toBeVisible();
  expect(screen.getByText("full-session-identifier")).toBeVisible();
});
it("session details are available with keyboard focus and Escape dismisses the card", async () => {
  render(<WorkspaceInfo session={session} kind="session" />);
  await userEvent.tab();
  expect(await screen.findByText("Session details")).toBeVisible();
  expect(screen.getByText("/projects/workspace")).toBeVisible();
  expect(screen.getByText("Started")).toBeVisible();
  await userEvent.keyboard("{Escape}");
  expect(screen.queryByText("Session details")).not.toBeInTheDocument();
});
it("inventory refresh preserves the open hover card", async () => {
  const props = {sessions: [session], selected: new Set<string>(), onSelect: () => {}, now: Date.now()};
  const view = render(<MemoryRouter><Workspaces {...props} /></MemoryRouter>);
  await userEvent.hover(screen.getByRole("button", {name: "Terminal details: /dev/ttys005"}));
  expect(await screen.findByText("Terminal details")).toBeVisible();
  view.rerender(<MemoryRouter><Workspaces {...props} sessions={structuredClone(props.sessions)} now={props.now + 3000} /></MemoryRouter>);
  expect(screen.getByText("Terminal details")).toBeVisible();
});
