import { MemoryRouter } from "react-router";
import { it, expect, vi } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Workspaces } from "../pages/Workspaces";
import { App } from "../App";
import { api } from "../api/client";
import { sessionStale, type Session, type Inventory } from "../api/types";
const session = (patch: Partial<Session> = {}): Session => ({
  identity: {
    session_id: "exact",
    workspace_id: "workspace",
    protocol_version: 1,
    capabilities: ["snapshot"],
  },
  root: "/owned/fixture",
  pid: 123,
  started_at: new Date().toISOString(),
  available: true,
  stale: false,
  last_seen: new Date().toISOString(),
  snapshot: {
    identity: {
      session_id: "exact",
      workspace_id: "workspace",
      protocol_version: 1,
      capabilities: [],
    },
    revision: 1,
    observed_at: new Date().toISOString(),
    nodes: [],
    processes: [],
    containers: [],
    diagnostics: [],
  },
  ...patch,
});
const total = {
  count: 0,
  known_memory_count: 0,
  known_cpu_count: 0,
  memory_bytes: null,
  cpu_percent: null,
  partial: false,
  sampled_at: null,
};
const inventory = (patch: Partial<Inventory> = {}): Inventory => ({
  sessions: [],
  resources: [],
  totals: { processes: total, containers: total },
  partial: false,
  collected_at: new Date().toISOString(),
  event_cursor: "event:1",
  ...patch,
});
function app() {
  vi.stubGlobal(
    "EventSource",
    class {
      addEventListener() {}
      close() {}
    },
  );
  const cache = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={cache}>
      <App />
    </QueryClientProvider>,
  );
}
it("initial auth and inventory loading are explicit", async () => {
  vi.spyOn(api, "bootstrap").mockResolvedValue({
    csrf: "c",
    expires_at: "2099",
  });
  vi.spyOn(api, "inventory").mockImplementation(() => new Promise(() => {}));
  app();
  expect(screen.getByText("Connecting to local control…")).toBeVisible();
  expect(
    await screen.findByText(
      "Waiting for the first successful inventory refresh…",
    ),
  ).toBeVisible();
});
it("healthy empty inventory has clear foreground guidance", async () => {
  vi.spyOn(api, "bootstrap").mockResolvedValue({
    csrf: "c",
    expires_at: "2099",
  });
  vi.spyOn(api, "inventory").mockResolvedValue(inventory());
  app();
  expect(await screen.findByText("No foreground sessions")).toBeVisible();
});
it("collection failure with zero rows stays failed partial state", async () => {
  vi.spyOn(api, "bootstrap").mockResolvedValue({
    csrf: "c",
    expires_at: "2099",
  });
  vi.spyOn(api, "inventory").mockResolvedValue(
    inventory({
      partial: true,
      collection_error: {
        code: "unavailable",
        message: "Inventory discovery unavailable",
      },
    }),
  );
  app();
  expect(
    await screen.findByText("Inventory discovery unavailable"),
  ).toBeVisible();
  expect(
    screen.getByText(/Retained records and totals may be incomplete/),
  ).toBeVisible();
});
it("stale and unsupported sessions cannot be selected for controls", () => {
  const stale = session({ stale: true });
  const unsupported = session({
    identity: {
      ...stale.identity,
      session_id: "future-generation",
      protocol_version: 2,
    },
    root: "/owned/unsupported",
  });
  render(
    <MemoryRouter><Workspaces
      sessions={[stale, unsupported]}
      selected={new Set()}
      onSelect={() => {}}
      now={Date.now()}
    /></MemoryRouter>,
  );
  expect(screen.getByText("stale")).toBeVisible();
  expect(screen.getByText("unsupported")).toBeVisible();
  expect(
    screen
      .getAllByRole("checkbox")
      .every(
        (c) =>
          (c as HTMLInputElement).getAttribute("data-disabled") !== null ||
          c.hasAttribute("disabled"),
      ),
  ).toBe(true);
});
it("client freshness ages out even if inventory transport stops updating", () => {
  const s = session();
  expect(sessionStale(s, Date.parse(s.snapshot!.observed_at) + 5000)).toBe(
    false,
  );
  expect(sessionStale(s, Date.parse(s.snapshot!.observed_at) + 5001)).toBe(
    true,
  );
});
it("filters workspace rows using TanStack9", async () => {
  render(
    <MemoryRouter><Workspaces
      sessions={[
        session(),
        session({
          root: "/owned/other",
          identity: { ...session().identity, session_id: "other" },
        }),
      ]}
      selected={new Set()}
      onSelect={() => {}}
      now={Date.now()}
    /></MemoryRouter>,
  );
  fireEvent.change(screen.getByLabelText("Filter workspaces"), {
    target: { value: "fixture" },
  });
  await waitFor(() =>
    expect(screen.queryByText("/owned/other")).not.toBeInTheDocument(),
  );
  expect(screen.getByText("/owned/fixture")).toBeVisible();
});

it("bulk requests retain independent outcomes and never call partial success overall success", async () => {
  vi.spyOn(api, "bootstrap").mockResolvedValue({
    csrf: "c",
    expires_at: "2099",
  });
  const first = session();
  const second = session({
    root: "/owned/second",
    identity: { ...first.identity, session_id: "second" },
  });
  vi.spyOn(api, "inventory").mockResolvedValue(
    inventory({ sessions: [first, second] }),
  );
  vi.spyOn(api, "plan").mockImplementation(async (sid, action) => ({
    id: "plan-" + sid,
    session_id: sid,
    action,
    targets: [],
    affected: ["app/api"],
    expires_at: "2099-01-01T00:00:00Z",
    fingerprint: "f",
    warnings: [],
  }));
  vi.spyOn(api, "submit").mockImplementation(async (p) => {
    if (p.session_id === "second")
      throw new Error(
        "Request outcome unknown. Inspect Operations before any new action.",
      );
    return {
      id: "op-first",
      session_id: p.session_id,
      action: "close",
      state: "queued",
      results: [],
      created_at: "now",
      updated_at: "now",
    };
  });
  app();
  await screen.findByRole("region", {name: "Workspaces"});
  fireEvent.click(screen.getByRole("link", { name: /^Workspaces$/ }));
  await screen.findByText("/owned/fixture");
  fireEvent.click(
    screen.getByRole("checkbox", { name: "Select /owned/fixture" }),
  );
  fireEvent.click(
    screen.getByRole("checkbox", { name: "Select /owned/second" }),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Review close selected" }),
  );
  await screen.findByRole("dialog");
  expect(screen.getByText(/2 independent session plans/)).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Confirm close" }));
  expect(
    await screen.findByText(/Some requests require attention/),
  ).toBeVisible();
  expect(api.submit).toHaveBeenCalledTimes(2);
  expect(screen.getByRole("button", { name: "Confirm close" })).toBeDisabled();
  expect(
    screen.queryByText("All operations succeeded"),
  ).not.toBeInTheDocument();
});
