import { usePreferences } from "../lib/preferences";
import { useLayoutEffect, useRef, useState } from "react";
import type { LogEntry } from "../api/types";
import { Button } from "./ui/button";
import { Notice } from "./Notice";
import { Empty, EmptyHeader, EmptyTitle, EmptyDescription } from "./ui/empty";
import { Input } from "./ui/input";
import { Field } from "./ui/field";
import { Select, SelectTrigger, SelectValue, SelectContent, SelectGroup, SelectItem } from "./ui/select";
import { CopyButton } from "./CopyButton";
import { matchesSearch } from "../lib/list-state";
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
  const output = useRef<HTMLDivElement>(null);
  const [search, setSearch] = useState("");
  const [stream, setStream] = useState("all");
  // Task streams retain their phase (e.g. checking/stdout); filter by the output channel.
  const shown = (paused ? frozen : entries).filter(entry =>
    (stream === "all" || entry.stream === stream || entry.stream.endsWith(`/${stream}`)) && matchesSearch(`${entry.service_id} ${entry.stream} ${entry.text}`, search));
  const added = paused ? entries.filter(entry => entry.sequence > (frozen.at(-1)?.sequence ?? 0)).length : 0;
  const pause = () => { setFrozen(entries); setPaused(true); };
  const resume = () => {
    setPaused(false);
    if (output.current) output.current.scrollTop = output.current.scrollHeight;
  };
  useLayoutEffect(() => {
    if (!paused && output.current) output.current.scrollTop = output.current.scrollHeight;
  }, [entries, paused, search, stream]);
  return (
    <div className="log-view">
      <div className="toolbar">
        <span>
          {paused ? tr("View paused") : connected ? tr("Following session output") : tr("Waiting for connection")} ·{" "}
          {entries.length}{" "}{tr("retained here")}{" "}</span>
        <Button
          variant="outline"
          onClick={paused ? resume : pause}
        >
          {paused ? tr("Resume following") : tr("Pause following")}
        </Button>
      </div>
      <div className="log-tools">
        <Field className="filter-field"><Input aria-label={tr("Search logs")} placeholder={tr("Search logs")} value={search} onChange={event => setSearch(event.target.value)} /></Field>
        <Select value={stream} onValueChange={setStream}>
          <SelectTrigger aria-label={tr("Output stream")}><SelectValue /></SelectTrigger>
          <SelectContent><SelectGroup>{["all", "stdout", "stderr"].map(value => <SelectItem key={value} value={value}>{value === "all" ? tr("All streams") : value}</SelectItem>)}</SelectGroup></SelectContent>
        </Select>
        <CopyButton label="Copy visible logs" value={shown.map(entry => `${entry.time} ${entry.service_id} / ${entry.stream} ${entry.text}`).join("\n")} />
      </div>
      {gap > 0 ? (
        <Notice title={tr("Log history gap")}>
          {gap}{" "}{tr("log entries are unavailable from retained history.")}{" "}</Notice>
      ) : null}
      {unavailable ? (
        <Notice title={tr("Session unavailable")}>{unavailable}</Notice>
      ) : null}
      {paused ? (
        <div className="toolbar"><p className="caption">{tr("View paused. Session log collection continues.")}</p>
          <Button variant="outline" onClick={resume}>{added ? tr("{count} new entries · Jump to latest", {count: added}) : tr("Jump to latest")}</Button>
        </div>
      ) : null}
      <div ref={output} className="log-lines" tabIndex={0} aria-label={tr("Session log output")}
        onWheel={event => { if (event.deltaY < 0 && !paused && event.currentTarget.scrollHeight > event.currentTarget.clientHeight) pause(); }}
        onScroll={event => {
          const element = event.currentTarget;
          if (!paused && element.scrollHeight - element.clientHeight - element.scrollTop > 24) pause();
        }}>
        {shown.length ? (
          shown.map((e) => (
            <div className="log-line" key={e.sequence}>
              <time dateTime={e.time}>{new Date(e.time).toLocaleTimeString(language)}</time>
              <span className="log-source" data-stream={e.stream}>
                {e.service_id} / {e.stream}
              </span>
              <span>{e.text}</span>
            </div>
          ))
        ) : (
          <Empty>
            <EmptyHeader>
              <EmptyTitle>{tr(entries.length ? "No matching records" : "No output yet")}</EmptyTitle>
              <EmptyDescription>{tr(entries.length ? "Try another filter." : "Output from this exact session will appear here.")}{" "}</EmptyDescription>
            </EmptyHeader>
          </Empty>
        )}
      </div>
    </div>
  );
}
