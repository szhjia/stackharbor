import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiFailure } from "./client";
import { mergeLogPage, logKey } from "./logs";
import type { LogPage, LogState } from "./types";
export function useEvents(
  enabled: boolean,
  initialCursor: string | undefined,
  selection: { session: string; target: string } | undefined,
  onAuthExpired: () => void,
) {
  const query = useQueryClient();
  const [status, setStatus] = useState("Connecting");
  const cursor = useRef("");
  const expired = useRef(onAuthExpired);
  expired.current = onAuthExpired;
  const sid = selection?.session;
  const target = selection?.target ?? "";
  useEffect(() => {
    if (!enabled || !initialCursor) return;
    let disposed = false;
    let source: EventSource | undefined;
    let timer: ReturnType<typeof setTimeout> | undefined;
    cursor.current = initialCursor;
    const revalidateUnavailableLogs = () => {
      if (!sid) return;
      const key = logKey(sid, target);
      const logs = query.getQueryData<LogState>(key);
      if (logs?.unavailable || query.getQueryState(key)?.status === "error") {
        void query.invalidateQueries({
          queryKey: key,
          exact: true,
          refetchType: "active",
        });
      }
    };
    const invalidate = () => {
      void query.invalidateQueries({ queryKey: ["inventory"] });
      void query.invalidateQueries({ queryKey: ["operations"] });
      void query.invalidateQueries({ queryKey: ["operation"] });
      revalidateUnavailableLogs();
    };
    const connect = () => {
      if (disposed) return;
      const params = new URLSearchParams({ after: cursor.current });
      if (sid) {
        params.set("session_id", sid);
        params.set("target", target);
        const log = query.getQueryData<LogState>(logKey(sid, target));
        if (log?.cursor) params.set("log_after", log.cursor);
      }
      source = new EventSource("/api/v1/events?" + params.toString());
      source.onopen = () => {
        setStatus("Live");
        revalidateUnavailableLogs();
      };
      const event = (name: string, callback: (data: any) => void) =>
        source!.addEventListener(name, (e) => {
          const m = e as MessageEvent;
          if (m.lastEventId) cursor.current = m.lastEventId;
          try {
            callback(JSON.parse(m.data));
          } catch {
            setStatus("Reconnecting");
            invalidate();
          }
        });
      event("inventory", invalidate);
      event("reset", (d) => {
        invalidate();
        if (d.reason === "log_cursor_changed" && sid) {
          query.setQueryData(logKey(sid, target), {
            entries: [],
            cursor: "",
            gap: 0,
          });
          source?.close();
          timer = setTimeout(connect, 0);
        }
      });
      event("gap", (p: LogPage) => {
        if (sid && p.session_id === sid && p.target === target)
          query.setQueryData<LogState>(logKey(sid, target), (old) => ({
            ...old,
            entries: old?.entries ?? [],
            cursor: old?.cursor ?? "",
            gap: p.dropped,
          }));
      });
      event("log", (p: LogPage) => {
        if (sid && p.session_id === sid && p.target === target)
          query.setQueryData<LogState>(logKey(sid, target), (old) => ({
            entries: mergeLogPage(old?.entries ?? [], p),
            cursor: p.cursor,
            gap: p.gap ? p.dropped : (old?.gap ?? 0),
          }));
      });
      event("session_unavailable", (d) => {
        if (sid && d.session_id === sid)
          query.setQueryData<LogState>(logKey(sid, target), (old) => ({
            ...old,
            entries: old?.entries ?? [],
            cursor: old?.cursor ?? "",
            gap: old?.gap ?? 0,
            unavailable: d.error.message,
          }));
        invalidate();
      });
      source.onerror = () => {
        source?.close();
        setStatus("Reconnecting");
        timer = setTimeout(async () => {
          try {
            await api.session();
            if (!disposed) {
              invalidate();
              connect();
            }
          } catch (e) {
            if (disposed) return;
            if (e instanceof ApiFailure && e.status === 401) {
              setStatus("Authentication expired");
              expired.current();
            } else timer = setTimeout(connect, 2000);
          }
        }, 1000);
      };
    };
    connect();
    return () => {
      disposed = true;
      source?.close();
      clearTimeout(timer);
    };
    // Bootstrap cursor seeds this stream; later invalidations keep the received cursor.
  }, [enabled, Boolean(initialCursor), sid, target, query]);
  return status;
}
