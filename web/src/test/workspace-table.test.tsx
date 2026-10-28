import {fireEvent, render, screen, within} from "@testing-library/react";
import {MemoryRouter} from "react-router";
import {expect, it} from "vitest";
import type {Node, Session, Resource} from "../api/types";
import {Workspaces} from "../pages/Workspaces";
import {Resources} from "../pages/Resources";
import {summarizeWorkspaceNodes} from "../lib/workspace-summary";

const metric = {known: false, partial: false, sampled_at: null, rss_bytes: null, cpu_percent: null, uptime_millis: null};
const identity = {session_id: "session-a", workspace_id: "workspace", protocol_version: 1, capabilities: []};
const node: Node = {id: "api", name: "API", project_id: "app", kind: "service", state: "running", reason: "", ownership: "session", allowed_actions: [], resource_refs: [], depends_on: [], metric, ports: []};
const session: Session = {identity, root: "/projects/long-parent/api", terminal: "Terminal", tty: "/dev/ttys001", pid: 123, available: true, stale: false, last_seen: null, started_at: new Date().toISOString(), snapshot: {
  identity, revision: 1, observed_at: new Date().toISOString(), nodes: [node, {...node, id: "web", state: "blocked"}, {...node, id: "build", kind: "task", state: "failed"}, {...node, id: "test", kind: "task", state: "running"}], processes: [], containers: [], diagnostics: [],
}};
it("flattens workspace identity and node counts into individually readable columns", () => {
  render(<MemoryRouter><Workspaces sessions={[session]} now={Date.now()} /></MemoryRouter>);
  const cells = within(screen.getAllByRole("row")[1]).getAllByRole("cell");
  expect(cells.map(cell => cell.textContent).slice(0, 9)).toEqual(["api", "Terminal", "123", "session-a", "Live", "2", "2", "1", "2"]);
  expect(within(cells[0]).getByRole("link", {name: session.root})).toHaveAttribute("href", "/workspaces/session-a");
  expect(screen.queryByText(session.root)).not.toBeInTheDocument();
  expect(screen.queryByText(session.tty!)).not.toBeInTheDocument();
  for (const query of ["/projects/long-parent", "/dev/ttys001", "123"]) {
    fireEvent.change(screen.getByRole("textbox"), {target: {value: query}});
    expect(screen.getByRole("link", {name: session.root})).toBeVisible();
  }
});
it("distinguishes an unavailable node snapshot from a known empty workspace", () => {
  render(<MemoryRouter><Workspaces sessions={[{...session, snapshot: undefined}, {...session, identity: {...identity, session_id: "empty"}, snapshot: {...session.snapshot!, nodes: []}}]} now={Date.now()} /></MemoryRouter>);
  const rows = screen.getAllByRole("row").slice(1);
  expect(within(rows[0]).getAllByRole("cell").slice(5, 9).map(cell => cell.textContent)).toEqual(["Unknown", "Unknown", "Unknown", "Unknown"]);
  expect(within(rows[1]).getAllByRole("cell").slice(5, 9).map(cell => cell.textContent)).toEqual(["0", "0", "0", "0"]);
});
it("labels non-task totals separately from probe-verified service readiness", () => {
  const nodes: Node[] = [node, {...node, id: "unprobed", state: "started"}, {...node, id: "db", kind: "resource", state: "available"}, {...node, id: "task", kind: "task", state: "succeeded"}];
  expect(summarizeWorkspaceNodes(nodes)).toEqual({services: 3, tasks: 1, runningServices: 1, abnormal: 0});
  render(<MemoryRouter><Workspaces sessions={[{...session, snapshot: {...session.snapshot!, nodes}}]} now={Date.now()} /></MemoryRouter>);
  expect(screen.getByRole("columnheader", {name: "Service / resource nodes"})).toBeVisible();
  expect(screen.getByRole("columnheader", {name: "Ready services"})).toBeVisible();
  expect(screen.getByText(/Node totals include services and registered resources/)).toBeVisible();
  expect(screen.queryByRole("button", {name: /close selected/i})).not.toBeInTheDocument();
});
it("counts health and identity problems as abnormal nodes without counting healthy or transitional states", () => {
  const states = ["failed", "error", "blocked", "unready", "unavailable", "unhealthy", "unknown", "running", "available", "started", "succeeded", "stopped", "waiting", "starting", "stopping", "checking", "running-task", "verifying"];
  const nodes = states.map(state => ({...node, id: state, state}));
  expect(summarizeWorkspaceNodes(nodes)?.abnormal).toBe(7);
  render(<MemoryRouter><Workspaces sessions={[{...session, snapshot: {...session.snapshot!, nodes}}]} now={Date.now()} /></MemoryRouter>);
  expect(within(screen.getAllByRole("row")[1]).getAllByRole("cell")[8]).toHaveTextContent(/^7$/);
});
it("shows compact workspace names in resources without duplicating shared physical rows", () => {
  const second = {...session, root: "/other/api", identity: {...identity, session_id: "session-b"}};
  const resource: Resource = {id: "process:123", pid: 123, kind: "process", identity_known: true, metric, references: [session, second].map(s => ({session_id: s.identity.session_id, workspace_id: "workspace", node_ids: ["api"], nodes: [{id: "api", state: "running", ownership: "session", role: "owner"}]}))};
  render(<MemoryRouter><Resources resources={[resource]} sessions={[session, second]} /></MemoryRouter>);
  expect(screen.getAllByRole("row")).toHaveLength(2);
  const cells = within(screen.getAllByRole("row")[1]).getAllByRole("cell");
  const links = within(cells[2]).getAllByRole("link");
  expect(links.map(link => link.textContent)).toEqual(["api", "api"]);
  expect(links.map(link => link.getAttribute("href"))).toEqual(["/workspaces/session-a", "/workspaces/session-b"]);
  expect(links.map(link => link.getAttribute("title"))).toEqual([session.root, second.root]);
  expect(within(cells[3]).getAllByRole("button")).toHaveLength(2);
  fireEvent.click(within(cells[3]).getByRole("button", {name: `Show 1 nodes for ${session.root}`}));
  expect(within(cells[3]).getByRole("link", {name: session.root})).toHaveAttribute("href", "/workspaces/session-a");
  expect(within(cells[3]).getByRole("link", {name: "Logs for api"})).toHaveAttribute("href", "/workspaces/session-a/logs?target=api");
  expect(screen.getAllByRole("row")).toHaveLength(2);
});
