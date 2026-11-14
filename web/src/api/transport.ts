import { isDesktop, nativeInvoke, withDeadline } from "../desktop/bridge";

export async function transportRequest(path: string, init: RequestInit, fetcher: typeof fetch): Promise<Response> {
  if (!isDesktop()) return fetcher(path, init);
  const result = await withDeadline(nativeInvoke<{ status: number; body: string }>("desktop_request", {
    method: init.method ?? "GET",
    path,
    ...(typeof init.body === "string" ? { body: init.body } : {}),
  }));
  return new Response(result.body, { status: result.status, headers: { "Content-Type": "application/json" } });
}

export interface StreamMessage { event: string; id: string; data: string }
export interface EventStream {
  onopen: (() => void) | null;
  onerror: (() => void) | null;
  addEventListener(name: string, callback: (event: MessageEvent) => void): void;
  close(): void;
}

/** One subscription, explicitly owned by the caller. Reconnection lives in useEvents. */
export function openEventStream(path: string): EventStream {
  if (!isDesktop()) return new EventSource(path) as unknown as EventStream;
  let closed = false;
  let id: string | number | undefined;
  let channel: { onmessage: (message: StreamMessage) => void } | undefined;
  let timeout: ReturnType<typeof setTimeout> | undefined;
  const listeners = new Map<string, (event: MessageEvent) => void>();
  const unsubscribe = () => {
    if (id === undefined) return;
    const subscription = id;
    id = undefined;
    // A failed unsubscribe must not create another subscription or retry a mutation.
    void nativeInvoke("desktop_unsubscribe", { id: subscription }).catch(() => {});
  };
  const source: EventStream = {
    onopen: null,
    onerror: null,
    addEventListener(name, callback) { listeners.set(name, callback); },
    close() {
      if (closed) return;
      closed = true;
      clearTimeout(timeout);
      listeners.clear();
      if (channel) channel.onmessage = () => {};
      unsubscribe();
    },
  };
  const fail = () => { if (!closed) source.onerror?.(); };
  timeout = setTimeout(fail, 11000);
  void import("@tauri-apps/api/core").then(async ({ Channel }) => {
    if (closed) return;
    channel = new Channel<StreamMessage>((message) => {
      if (closed) return;
      if (message.event === "error" || message.event === "closed") { fail(); return; }
      listeners.get(message.event)?.({ data: message.data, lastEventId: message.id } as MessageEvent);
    });
    id = await nativeInvoke<string | number>("desktop_subscribe", { path, onEvent: channel });
    clearTimeout(timeout);
    // Visibility may have changed while native subscription creation was pending.
    if (closed) unsubscribe();
    else source.onopen?.();
  }).catch(() => { clearTimeout(timeout); fail(); });
  return source;
}
