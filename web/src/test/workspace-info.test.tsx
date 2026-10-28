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
it("session details retain the full workspace and terminal identity outside the compact row", async () => {
  render(<WorkspaceInfo session={session} />);
  const trigger = screen.getByRole("button", {name: "Session details: full-session-identifier"});
  await userEvent.hover(trigger);
  expect(await screen.findByText("Session details")).toBeVisible();
  expect(screen.getByText("Started")).toBeVisible();
  expect(screen.getByText("/projects/workspace")).toBeVisible();
  expect(screen.getByText("/dev/ttys005")).toBeVisible();
  expect(screen.getByText("1234")).toBeVisible();
  await userEvent.keyboard("{Escape}");
  expect(screen.queryByText("Session details")).not.toBeInTheDocument();
});
it("workspace rows keep terminal identity visible and the hover card open across inventory refresh", async () => {
  const props = {sessions: [session], now: Date.now()};
  const view = render(<MemoryRouter><Workspaces {...props} /></MemoryRouter>);
  expect(screen.getByText("Foreground session")).toBeVisible();
  expect(screen.getByText("1234")).toBeVisible();
  expect(screen.queryByText("/projects/workspace")).not.toBeInTheDocument();
  await userEvent.hover(screen.getByRole("button", {name: "Session details: full-session-identifier"}));
  expect(await screen.findByText("Session details")).toBeVisible();
  view.rerender(<MemoryRouter><Workspaces {...props} sessions={structuredClone(props.sessions)} now={props.now + 3000} /></MemoryRouter>);
  expect(screen.getByText("Session details")).toBeVisible();
});
