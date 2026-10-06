import type { LogEntry, LogPage } from "./types";
export function mergeLogPage(entries: LogEntry[], page: LogPage) {
  const map = new Map((page.reset ? [] : entries).map((e) => [e.sequence, e]));
  for (const e of page.entries) map.set(e.sequence, e);
  return [...map.values()].sort((a, b) => a.sequence - b.sequence).slice(-2000);
}
export const logKey = (sid: string, target: string) =>
  ["logs", sid, target] as const;
