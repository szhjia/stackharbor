import {Link} from "react-router";
import {workspacePath} from "../routes";

// The exact session owns navigation; the full root stays discoverable while
// long paths never determine the table's column width.
export function WorkspaceLink({sessionID, root}: {sessionID: string; root: string}) {
  return <Link className="workspace-link" aria-label={root} title={root} to={workspacePath(sessionID)}>
    {root.split("/").filter(Boolean).at(-1) || root}
  </Link>;
}
