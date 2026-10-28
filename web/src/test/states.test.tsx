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
it("stale and unsupported sessions are labeled where connection state is shown", () => {
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
      now={Date.now()}
    /></MemoryRouter>,
  );
  expect(screen.getByText("stale")).toBeVisible();
  expect(screen.getByText("unsupported", {selector: '[data-slot="badge"]'})).toBeVisible();
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
      now={Date.now()}
    /></MemoryRouter>,
  );
  fireEvent.change(screen.getByLabelText("Filter workspaces"), {
    target: { value: "fixture" },
  });
  await waitFor(() =>
    expect(screen.queryByRole("link", {name: "/owned/other"})).not.toBeInTheDocument(),
  );
  expect(screen.getByRole("link", {name: "/owned/fixture"})).toBeVisible();
});
