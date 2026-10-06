import { useEffect, useRef, useState } from "react";
import type { LogEntry } from "../api/types";
import { Button } from "./ui/button";
import { Notice } from "./Notice";
import { Empty, EmptyHeader, EmptyTitle, EmptyDescription } from "./ui/empty";
export function LogView({
  entries,
  gap,
  connected,
  unavailable,
}: {
  entries: LogEntry[];
  gap: number;
  connected: boolean;
  unavailable?: string;
}) {
  const [paused, setPaused] = useState(false);
  const [frozen, setFrozen] = useState<LogEntry[]>([]);
  const end = useRef<HTMLDivElement>(null);
  const shown = paused ? frozen : entries;
  useEffect(() => {
    if (!paused) end.current?.scrollIntoView?.({ block: "nearest" });
  }, [entries, paused]);
  return (
    <div className="log-view">
      <div className="toolbar">
        <span>
          {connected ? "Following session output" : "Waiting for connection"} ·{" "}
          {entries.length} retained here
        </span>
        <Button
          variant="outline"
          onClick={() => {
            setFrozen(entries);
            setPaused(!paused);
          }}
        >
          {paused ? "Resume following" : "Pause following"}
        </Button>
      </div>
      {gap > 0 ? (
        <Notice title="Log history gap">
          {gap} log entries are unavailable from retained history.
        </Notice>
      ) : null}
      {unavailable ? (
        <Notice title="Session unavailable">{unavailable}</Notice>
      ) : null}
      {paused ? (
        <p className="caption">
          View paused. Session log collection continues.
        </p>
      ) : null}
      <div className="log-lines" aria-label="Session log output">
        {shown.length ? (
          shown.map((e) => (
            <div className="log-line" key={e.sequence}>
              <time>{new Date(e.time).toLocaleTimeString()}</time>
              <span className="log-source">
                {e.service_id} / {e.stream}
              </span>
              <span>{e.text}</span>
            </div>
          ))
        ) : (
          <Empty>
            <EmptyHeader>
              <EmptyTitle>No output yet</EmptyTitle>
              <EmptyDescription>
                Output from this exact session will appear here.
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        )}
        <div ref={end} />
      </div>
    </div>
  );
}
