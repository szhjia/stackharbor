import { usePreferences } from "../lib/preferences";
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
  const {t: tr, language} = usePreferences();
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
          {connected ? tr("Following session output") : tr("Waiting for connection")} ·{" "}
          {entries.length}{" "}{tr("retained here")}{" "}</span>
        <Button
          variant="outline"
          onClick={() => {
            setFrozen(entries);
            setPaused(!paused);
          }}
        >
          {paused ? tr("Resume following") : tr("Pause following")}
        </Button>
      </div>
      {gap > 0 ? (
        <Notice title={tr("Log history gap")}>
          {gap}{" "}{tr("log entries are unavailable from retained history.")}{" "}</Notice>
      ) : null}
      {unavailable ? (
        <Notice title={tr("Session unavailable")}>{unavailable}</Notice>
      ) : null}
      {paused ? (
        <p className="caption">{tr("View paused. Session log collection continues.")}{" "}</p>
      ) : null}
      <div className="log-lines" aria-label={tr("Session log output")}>
        {shown.length ? (
          shown.map((e) => (
            <div className="log-line" key={e.sequence}>
              <time>{new Date(e.time).toLocaleTimeString(language)}</time>
              <span className="log-source">
                {e.service_id} / {e.stream}
              </span>
              <span>{e.text}</span>
            </div>
          ))
        ) : (
          <Empty>
            <EmptyHeader>
              <EmptyTitle>{tr("No output yet")}</EmptyTitle>
              <EmptyDescription>{tr("Output from this exact session will appear here.")}{" "}</EmptyDescription>
            </EmptyHeader>
          </Empty>
        )}
        <div ref={end} />
      </div>
    </div>
  );
}
