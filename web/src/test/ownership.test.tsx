import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { expect, it } from "vitest";
import { Resources } from "../pages/Resources";
import type { Resource } from "../api/types";

it("resource columns separate identifiers and metrics without hiding unknown or partial samples", async () => {
  const resource: Resource = {id: "process:123:exact-identity", pid: 123, kind: "process", identity_known: false,
    metric: {known: false, partial: true, sampled_at: null, rss_bytes: 500, cpu_percent: 2, uptime_millis: null}, references: []};
  render(<MemoryRouter><Resources resources={[resource]} sessions={[]} /></MemoryRouter>);
  expect(screen.getAllByRole("columnheader").map(e => e.textContent)).toEqual(["Type", "PID / ID", "Workspace / Nodes", "CPU", "Memory", "Sample"]);
  const cells = screen.getAllByRole("cell");
  expect(cells[3]).toHaveTextContent("2.0%");
  expect(cells[4]).toHaveTextContent("Unknown");
  expect(cells[5]).toHaveTextContent("Partial");
  await userEvent.hover(screen.getByRole("button", {name: "Resource details: process:123:exact-identity"}));
  expect(await screen.findByText("process:123:exact-identity")).toBeVisible();
  expect(screen.getByText("Identity").nextElementSibling).toHaveTextContent("Unknown");
});

it("ownership groups collapse independently, remain open after inventory updates and support searching hidden nodes", async () => {
  const reference = {session_id: "session", workspace_id: "workspace", node_ids: ["backend/api", "admin/web"], nodes: [
    {id: "backend/api", role: "owner", ownership: "session", state: "running"},
    {id: "admin/web", role: "consumer", ownership: "session", state: "running"},
  ]};
  const resources: Resource[] = [1, 2].map((pid) => ({id: `process:${pid}`, pid, kind: "process", identity_known: true,
    metric: {known: false, partial: false, sampled_at: null, rss_bytes: null, cpu_percent: null, uptime_millis: null}, references: [reference]}));
  const view = render(<MemoryRouter><Resources resources={resources} sessions={[]} /></MemoryRouter>);
  const rows = screen.getAllByRole("row").slice(1);
  const first = within(rows[0]);
  const second = within(rows[1]);
  expect(first.getByRole("button", {name: "Show 2 nodes for workspace"})).toHaveAttribute("aria-expanded", "false");
  expect(first.getByText("backend/api")).not.toBeVisible();
  await userEvent.click(first.getByRole("button", {name: "Show 2 nodes for workspace"}));
  expect(first.getByText("backend/api")).toBeVisible();
  expect(second.getByText("backend/api")).not.toBeVisible();
  view.rerender(<MemoryRouter><Resources resources={structuredClone(resources)} sessions={[]} /></MemoryRouter>);
  expect(screen.getByRole("button", {name: "Hide 2 nodes for workspace"})).toHaveAttribute("aria-expanded", "true");
  await userEvent.click(screen.getByRole("button", {name: "Hide 2 nodes for workspace"}));
  await userEvent.type(screen.getByRole("textbox"), "backend/api");
  expect(screen.getAllByRole("row")).toHaveLength(3);
  expect(screen.getAllByRole("button", {name: "Show 2 nodes for workspace"})).toHaveLength(2);
});
