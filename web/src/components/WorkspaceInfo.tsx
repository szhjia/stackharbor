import { usePreferences } from "../lib/preferences";
import type { Session } from "../api/types";
import { InfoPopover } from "./InfoPopover";
import { DefinitionList, type DefinitionItem } from "./DefinitionList";

export function WorkspaceInfo({session}: {session: Session}) {
  const {t: tr, language} = usePreferences();
  const items: DefinitionItem[] = [
    {label: tr("Workspace"), value: session.root, mono: true},
    {label: tr("Session ID"), value: session.identity.session_id, mono: true},
    {label: tr("Terminal"), value: session.terminal || tr("Foreground session")},
    {label: "TTY", value: session.tty || tr("Not available"), mono: true},
    {label: "PID", value: session.pid, mono: true},
    {label: tr("Started"), value: new Date(session.started_at).toLocaleString(language)},
    {label: tr("Last observation"), value: session.snapshot?.observed_at ? new Date(session.snapshot.observed_at).toLocaleString(language) : tr("Unknown")},
    ...(session.error ? [{label: tr("Connection"), value: session.error.message}] : []),
  ];
  const title = tr("Session details");
  return <InfoPopover title={title} label={`${title}: ${session.identity.session_id}`}
    className="workspace-session-id mono"
    summary={<span className="truncate">{session.identity.session_id}</span>}>
    <DefinitionList items={items} />
  </InfoPopover>;
}
