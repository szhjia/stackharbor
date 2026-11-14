import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { waitFor } from "@testing-library/react";
import { ApiClient, ApiFailure } from "../api/client";
import { openEventStream, type StreamMessage } from "../api/transport";
import { desktop, isDesktop } from "../desktop/bridge";
import { rememberDesktopRoute, restoreDesktopRoute } from "../desktop/route-state";

const { invoke, channels } = vi.hoisted(() => ({ invoke: vi.fn(), channels: [] as { onmessage: (message: StreamMessage) => void }[] }));
vi.mock("@tauri-apps/api/core", () => ({
  invoke,
  Channel: class {
    onmessage: (message: StreamMessage) => void;
    constructor(callback: (message: StreamMessage) => void) { this.onmessage = callback; channels.push(this); }
  },
}));
beforeEach(() => { invoke.mockReset(); channels.length = 0; localStorage.clear(); });
afterEach(() => { delete window.__STACKHARBOR_DESKTOP__; vi.unstubAllGlobals(); vi.useRealTimers(); });

it("uses HTTP in browsers and legacy Tauri, and never exposes native system commands", async () => {
  vi.stubGlobal("__TAURI_INTERNALS__", {});
  const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { sessions: [] } })));
  expect(isDesktop()).toBe(false);
  expect(await new ApiClient(fetcher).inventory()).toEqual({ sessions: [] });
  expect(fetcher).toHaveBeenCalledWith("/api/v1/inventory", expect.objectContaining({ method: "GET", credentials: "same-origin" }));
  await expect(desktop.status()).rejects.toThrow("requires the installed");
  expect(invoke).not.toHaveBeenCalled();
});

it("uses the injected namespace marker and exact native request command without HTTP auth bootstrap", async () => {
  window.__STACKHARBOR_DESKTOP__ = { namespace: "personal" };
  const fetcher = vi.fn();
  invoke.mockResolvedValue({ status: 200, body: JSON.stringify({ data: { session_id: "native-session" } }) });
  const client = new ApiClient(fetcher);
  await client.bootstrap(new URL("https://tauri.localhost/workspaces"), vi.fn());
  expect(invoke).not.toHaveBeenCalled();
  expect(await client.openWorkspace("/work/api", "custom.yaml")).toEqual({ session_id: "native-session" });
  expect(invoke).toHaveBeenCalledExactlyOnceWith("desktop_request", { method: "POST", path: "/api/v1/workspaces/open", body: JSON.stringify({ root: "/work/api", workspace: "custom.yaml" }) });
  expect(fetcher).not.toHaveBeenCalled();
});

it.each([new Error("socket dropped"), { status: 200, body: "not json" }])("retains unknown native mutation outcomes without automatic replay (%s)", async failure => {
  window.__STACKHARBOR_DESKTOP__ = { namespace: "personal" };
  if (failure instanceof Error) invoke.mockRejectedValue(failure);
  else invoke.mockResolvedValue(failure);
  const result = new ApiClient(vi.fn()).openWorkspace("/workspace");
  await expect(result).rejects.toMatchObject({ code: "outcome_unknown", status: 0 });
  expect(invoke).toHaveBeenCalledTimes(1);
});

it("does not replay a native mutation rejected with 401", async () => {
  window.__STACKHARBOR_DESKTOP__ = { namespace: "personal" };
  invoke.mockResolvedValue({ status: 401, body: JSON.stringify({ error: { code: "unauthenticated", message: "unverified endpoint" } }) });
  await expect(new ApiClient(vi.fn()).addWorkspace("/workspace")).rejects.toMatchObject({ status: 401, code: "unauthenticated" });
  expect(invoke).toHaveBeenCalledTimes(1);
});

it("bounds a native request and preserves its unknown outcome without a retry", async () => {
  vi.useFakeTimers();
  window.__STACKHARBOR_DESKTOP__ = { namespace: "personal" };
  invoke.mockImplementation(() => new Promise(() => {}));
  const result = new ApiClient(vi.fn()).openWorkspace("/workspace").catch(error => error);
  await vi.advanceTimersByTimeAsync(11000);
  expect(await result).toBeInstanceOf(ApiFailure);
  expect((await result).code).toBe("outcome_unknown");
  expect(invoke).toHaveBeenCalledTimes(1);
});

it("delivers native SSE-equivalent events and unsubscribes exactly once", async () => {
  window.__STACKHARBOR_DESKTOP__ = { namespace: "personal" };
  invoke.mockResolvedValue("subscription-1");
  const source = openEventStream("/api/v1/events?after=global%3A1");
  const event = vi.fn(), opened = vi.fn(), failed = vi.fn();
  source.addEventListener("inventory", event);
  source.onopen = opened; source.onerror = failed;
  await waitFor(() => expect(opened).toHaveBeenCalledTimes(1));
  expect(invoke).toHaveBeenCalledWith("desktop_subscribe", { path: "/api/v1/events?after=global%3A1", onEvent: channels[0] });
  channels[0].onmessage({ event: "inventory", id: "global:2", data: "{}" });
  expect(event).toHaveBeenCalledWith(expect.objectContaining({ data: "{}", lastEventId: "global:2" }));
  channels[0].onmessage({ event: "error", id: "", data: "upstream disconnected" });
  expect(failed).toHaveBeenCalledTimes(1);
  source.close(); source.close();
  channels[0].onmessage({ event: "inventory", id: "global:3", data: "{}" });
  await waitFor(() => expect(invoke).toHaveBeenCalledWith("desktop_unsubscribe", { id: "subscription-1" }));
  expect(event).toHaveBeenCalledTimes(1);
  expect(invoke.mock.calls.filter(call => call[0] === "desktop_unsubscribe")).toHaveLength(1);
});

it("cleans up a subscription whose ID arrives after the window was hidden", async () => {
  window.__STACKHARBOR_DESKTOP__ = { namespace: "personal" };
  let finish!: (id: string) => void;
  invoke.mockImplementation((command: string) => command === "desktop_subscribe" ? new Promise(resolve => { finish = resolve; }) : Promise.resolve());
  const source = openEventStream("/api/v1/events");
  const opened = vi.fn(); source.onopen = opened;
  await waitFor(() => expect(invoke).toHaveBeenCalledWith("desktop_subscribe", expect.anything()));
  source.close();
  finish("late-id");
  await waitFor(() => expect(invoke).toHaveBeenCalledWith("desktop_unsubscribe", { id: "late-id" }));
  expect(opened).not.toHaveBeenCalled();
});

it("restores a native route and filters by namespace without accepting external or unknown routes", () => {
  window.__STACKHARBOR_DESKTOP__ = { namespace: "a" };
  rememberDesktopRoute("/workspaces/session/logs?target=api%2Fweb&q=failed");
  restoreDesktopRoute();
  expect(location.pathname + location.search).toBe("/workspaces/session/logs?target=api%2Fweb&q=failed");
  history.replaceState(null, "", "/");
  window.__STACKHARBOR_DESKTOP__ = { namespace: "b" };
  restoreDesktopRoute();
  expect(location.pathname).toBe("/");
  rememberDesktopRoute("//external.example/path");
  restoreDesktopRoute();
  expect(location.pathname).toBe("/");
  delete window.__STACKHARBOR_DESKTOP__;
  rememberDesktopRoute("/settings");
  expect(localStorage.getItem("stackharbor.desktop.route:a")).toContain("target=api%2Fweb");
});
