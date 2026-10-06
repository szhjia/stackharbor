import { MemoryRouter } from "react-router";
import { useState } from "react";
import userEvent from "@testing-library/user-event";
import { it, expect, vi } from "vitest";
import {
  renderHook,
  render,
  screen,
  fireEvent,
  act,
  waitFor,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useEvents } from "../api/events";
import { api, ApiFailure } from "../api/client";
import { WorkspaceDetail } from "../pages/WorkspaceDetail";
import { logKey } from "../api/logs";
class Source {
  static all: Source[] = [];
  listeners = new Map<string, (e: MessageEvent) => void>();
  onopen?: () => void;
  onerror?: () => void;
  close = vi.fn();
  constructor(public url: string) {
    Source.all.push(this);
  }
  addEventListener(n: string, f: (e: MessageEvent) => void) {
    this.listeners.set(n, f);
  }
  emit(n: string, data: unknown, id = "") {
    this.listeners.get(n)?.({
      data: JSON.stringify(data),
      lastEventId: id,
    } as MessageEvent);
  }
}
function setup() {
  Source.all = [];
  vi.stubGlobal("EventSource", Source);
  const cache = new QueryClient();
  const expired = vi.fn();
  renderHook(
    () =>
      useEvents(
        true,
        "global:1",
        { session: "exact", target: "app/api" },
        expired,
      ),
    {
      wrapper: ({ children }) => (
        <QueryClientProvider client={cache}>{children}</QueryClientProvider>
      ),
    },
  );
  return { cache, expired };
}
it("recreates SSE with latest event and log cursors after checking cookie", async () => {
  vi.useFakeTimers();
  vi.spyOn(api, "session").mockResolvedValue({
    csrf: "c",
    expires_at: "later",
  });
  const { cache } = setup();
  const source = Source.all[0];
  const p = {
    entries: [
      {
        sequence: 2,
        text: "safe",
        time: "now",
        project_id: "app",
        service_id: "app/api",
        stream: "stdout",
      },
    ],
    session_id: "exact",
    target: "app/api",
    cursor: "bound:2",
    gap: true,
    dropped: 1,
    next_cursor: 2,
    reset: false,
  };
  act(() => {
    source.emit("inventory", {}, "global:2");
    source.emit("gap", p);
    source.emit("log", p);
    source.onerror?.();
  });
  await act(async () => {
    await vi.advanceTimersByTimeAsync(1000);
  });
  expect(source.close).toHaveBeenCalled();
  expect(api.session).toHaveBeenCalled();
  const u = new URL(Source.all[1].url, "http://local");
  expect(u.searchParams.get("after")).toBe("global:2");
  expect(u.searchParams.get("log_after")).toBe("bound:2");
  expect(
    cache.getQueryData<any>(logKey("exact", "app/api")).entries,
  ).toHaveLength(1);
  vi.useRealTimers();
});
it("401 during stream recovery requests reauthentication", async () => {
  vi.useFakeTimers();
  vi.spyOn(api, "session").mockRejectedValue(
    new ApiFailure(401, "unauthenticated", "expired"),
  );
  const { expired } = setup();
  act(() => Source.all[0].onerror?.());
  await act(async () => {
    await vi.advanceTimersByTimeAsync(1000);
  });
  expect(expired).toHaveBeenCalledTimes(1);
  expect(Source.all).toHaveLength(1);
  vi.useRealTimers();
});

it.each(["inventory", "open"])(
  "quiet exact-session log recovery via %s clears unavailable only after a successful empty read",
  async (recovery) => {
    Source.all = [];
    vi.stubGlobal("EventSource", Source);
    const cache = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const entry = {
      sequence: 2,
      text: "retained",
      time: "2026-10-05T00:00:00Z",
      project_id: "app",
      service_id: "app/api",
      stream: "stdout",
    };
    const page = {
      entries: [entry],
      session_id: "exact",
      target: "",
      cursor: "bound:2",
      next_cursor: 2,
      gap: false,
      dropped: 0,
      reset: false,
    };
    const logs = vi
      .spyOn(api, "logs")
      .mockResolvedValueOnce(page)
      .mockRejectedValueOnce(
        new ApiFailure(503, "unavailable", "Log session unavailable"),
      );
    const session = {
      identity: {
        session_id: "exact",
        workspace_id: "workspace",
        protocol_version: 1,
        capabilities: [],
      },
      root: "/fixture",
      pid: 1,
      available: true,
      stale: false,
      last_seen: null,
      started_at: "now",
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
    };
    function Harness() {
      const [tab, setTab] = useState("services");
      useEvents(true, "global:1", { session: "exact", target: "" }, () => {});
      return (
        <WorkspaceDetail
          session={session}
          resources={[]}
          sessions={[session]}
          now={Date.now()}
          connected
          onPlan={() => {}}
          tab={tab}
          target=""
          onTabChange={setTab}
          onTargetChange={() => {}}
        />
      );
    }
    render(
      <QueryClientProvider client={cache}>
        <MemoryRouter><Harness /></MemoryRouter>
      </QueryClientProvider>,
    );
    await userEvent.click(screen.getByRole("tab", { name: "Logs" }));
    await screen.findByText("retained");
    act(() =>
      Source.all[0].emit("session_unavailable", {
        session_id: "exact",
        error: {
          code: "unavailable",
          message: "Log session unavailable; rediscover sessions",
        },
      }),
    );
    await waitFor(() => expect(logs).toHaveBeenCalledTimes(2));
    expect(
      cache.getQueryData<any>(logKey("exact", "")).unavailable,
    ).toBeTruthy();
    logs.mockResolvedValue({ ...page, entries: [] });
    act(() => {
      if (recovery === "open") Source.all[0].onopen?.();
      else Source.all[0].emit("inventory", {}, "global:2");
    });
    await waitFor(() =>
      expect(
        cache.getQueryData<any>(logKey("exact", "")).unavailable,
      ).toBeUndefined(),
    );
    expect(logs).toHaveBeenLastCalledWith("exact", "", "bound:2");
    expect(cache.getQueryData<any>(logKey("exact", "")).entries).toEqual([
      entry,
    ]);
    expect(cache.getQueryData<any>(logKey("exact", "")).cursor).toBe("bound:2");
    expect(screen.queryByText("Session unavailable")).not.toBeInTheDocument();
  },
);
