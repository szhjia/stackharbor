import { usePreferences } from "../lib/preferences";
import type { Session } from "../api/types";
import { InfoPopover } from "./InfoPopover";
import { DefinitionList, type DefinitionItem } from "./DefinitionList";

export function WorkspaceInfo({session, kind}: {session: Session; kind: "terminal" | "session"}) {
  const {t: tr, language} = usePreferences();
  const terminal = session.terminal || tr("Foreground session");
  const summary = kind === "session" ? session.identity.session_id : session.terminal || session.tty || terminal;
  const items: DefinitionItem[] = kind === "terminal" ? [
    {label: tr("Terminal"), value: terminal},
    {label: "TTY", value: session.tty || tr("Not available"), mono: true},
    {label: "PID", value: session.pid, mono: true},
    {label: tr("Session ID"), value: session.identity.session_id, mono: true},
  ] : [
    {label: tr("Session ID"), value: session.identity.session_id, mono: true},
    {label: tr("Workspace"), value: session.root},
    {label: tr("Started"), value: new Date(session.started_at).toLocaleString(language)},
    {label: tr("Last observation"), value: session.snapshot?.observed_at ? new Date(session.snapshot.observed_at).toLocaleString(language) : tr("Unknown")},
  ];
  const title = kind === "session" ? tr("Session details") : tr("Terminal details");
  return <InfoPopover title={title} label={`${title}: ${summary}`}
    className={kind === "session" ? "workspace-session-id mono" : "workspace-terminal"}
    summary={<span className="truncate">{summary}</span>}>
    <DefinitionList items={items} />
  </InfoPopover>;
}
