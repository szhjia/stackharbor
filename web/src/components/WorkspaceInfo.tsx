import type { Session } from "../api/types";
import { InfoPopover } from "./InfoPopover";
import { DefinitionList, type DefinitionItem } from "./DefinitionList";

export function WorkspaceInfo({session, kind}: {session: Session; kind: "terminal" | "session"}) {
  const terminal = session.terminal || "Foreground session";
  const summary = kind === "session" ? session.identity.session_id : session.terminal || session.tty || terminal;
  const items: DefinitionItem[] = kind === "terminal" ? [
    {label: "Terminal", value: terminal},
    {label: "TTY", value: session.tty || "Not available", mono: true},
    {label: "PID", value: session.pid, mono: true},
    {label: "Session ID", value: session.identity.session_id, mono: true},
  ] : [
    {label: "Session ID", value: session.identity.session_id, mono: true},
    {label: "Workspace", value: session.root},
    {label: "Started", value: new Date(session.started_at).toLocaleString()},
    {label: "Last observation", value: session.snapshot?.observed_at ? new Date(session.snapshot.observed_at).toLocaleString() : "Unknown"},
  ];
  const title = kind === "session" ? "Session details" : "Terminal details";
  return <InfoPopover title={title} label={`${title}: ${summary}`}
    className={kind === "session" ? "workspace-session-id mono" : "workspace-terminal"}
    summary={<span className="truncate">{summary}</span>}>
    <DefinitionList items={items} />
  </InfoPopover>;
}
