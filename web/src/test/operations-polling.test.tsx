import {act, cleanup, fireEvent, render, screen} from "@testing-library/react";
import {QueryClient, QueryClientProvider} from "@tanstack/react-query";
import {MemoryRouter} from "react-router";
import {afterEach, expect, it, vi} from "vitest";
import {Operations} from "../pages/Operations";
import {useEvents} from "../api/events";
import {api, ApiFailure} from "../api/client";
import type {Session, Operation} from "../api/types";

class Source {
  static current: Source;
  listeners = new Map<string, (event: MessageEvent) => void>();
  onopen?: () => void;
  close() {}
  constructor() { Source.current = this; }
  addEventListener(name: string, listener: (event: MessageEvent) => void) { this.listeners.set(name, listener); }
  inventory() { this.listeners.get("inventory")?.({data: "{}"} as MessageEvent); }
}
const identity = {session_id: "exact", workspace_id: "work", protocol_version: 1, capabilities: []};
const session: Session = {identity, root: "/fixture", pid: 1, available: true, stale: false, last_seen: null, started_at: "2026-10-16T00:00:00Z"};
const operation: Operation = {id: "op", session_id: "exact", action: "restart", state: "running", created_at: "2026-10-16T00:00:00Z", updated_at: "2026-10-16T00:00:00Z", results: []};
const unavailable = new ApiFailure(503, "unavailable", "operation outcome is unknown; session is unavailable");
let cache: QueryClient;
afterEach(() => { cleanup(); cache?.clear(); vi.useRealTimers(); vi.unstubAllGlobals(); });

async function mount(sessions: Session[] = []) {
  vi.useFakeTimers();
  vi.stubGlobal("EventSource", Source);
  vi.spyOn(api, "operations").mockResolvedValue([]);
  cache = new QueryClient({defaultOptions: {queries: {retry: false}}});
  function Harness() {
    useEvents(true, "event:1", undefined, () => {});
    return <Operations sessions={sessions} tracked={[{session_id: "exact", id: "op"}]} onError={() => {}} />;
  }
  render(<QueryClientProvider client={cache}><MemoryRouter><Harness /></MemoryRouter></QueryClientProvider>);
  await act(async () => { await vi.advanceTimersByTimeAsync(0); });
}
async function tick(seconds: number) {
  for (let i = 0; i < seconds; i++) await act(async () => {
    Source.current.inventory();
    await vi.advanceTimersByTimeAsync(1000);
    // Flush React Query's deferred observer notifications after the read.
    await vi.advanceTimersByTimeAsync(10);
  });
}

it("leaves an ended session's missing outcome unknown without polling it on every inventory update", async () => {
  const read = vi.spyOn(api, "operation").mockRejectedValue(unavailable);
  await mount();
  await tick(10);
  expect(read).toHaveBeenCalledTimes(1);
  expect(screen.getByText("Some operation outcomes are unavailable")).toBeVisible();
  expect(screen.queryByText("succeeded")).not.toBeInTheDocument();
});

it("backs off a live session's failed lookup despite SSE updates, then resumes polling until terminal", async () => {
  const read = vi.spyOn(api, "operation").mockRejectedValueOnce(unavailable)
    .mockResolvedValueOnce(operation).mockResolvedValue({...operation, state: "succeeded"});
  await mount([session]);
  await tick(1);
  expect(read).toHaveBeenCalledTimes(1);
  await tick(1);
  expect(screen.getByText("running")).toBeVisible();
  await tick(1);
  expect(screen.getByText("succeeded")).toBeVisible();
  const calls = read.mock.calls.length;
  await tick(5);
  expect(read).toHaveBeenCalledTimes(calls);
});

it("lets a user retry a paused missing outcome and clears the warning only after success", async () => {
  const read = vi.spyOn(api, "operation").mockRejectedValueOnce(unavailable).mockResolvedValue({...operation, state: "succeeded"});
  await mount();
  fireEvent.click(screen.getByRole("button", {name: "Retry unavailable outcomes"}));
  await act(async () => { await vi.advanceTimersByTimeAsync(0); });
  expect(read).toHaveBeenCalledTimes(2);
  expect(screen.getByText("succeeded")).toBeVisible();
  expect(screen.queryByText("Some operation outcomes are unavailable")).not.toBeInTheDocument();
});

it("checks unavailable outcomes once when the event connection returns", async () => {
  vi.spyOn(api, "operation").mockRejectedValueOnce(unavailable).mockResolvedValue({...operation, state: "succeeded"});
  await mount();
  await act(async () => { Source.current.onopen?.(); await vi.advanceTimersByTimeAsync(0); });
  expect(screen.getByText("succeeded")).toBeVisible();
});
