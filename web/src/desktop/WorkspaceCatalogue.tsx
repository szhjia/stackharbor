import { useState } from "react";
import { Link, useNavigate } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, actionableError, ApiFailure } from "../api/client";
import type { WorkspaceEntry } from "../api/types";
import { workspacePath } from "../routes";
import { Panel } from "../components/Panel";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import { Notice } from "../components/Notice";
import { Badge } from "../components/ui/badge";
import { desktop, isDesktop } from "./bridge";
import { usePreferences } from "../lib/preferences";
import { desktopMessage } from "./messages";
import "./desktop.css";

export function WorkspaceCatalogue() {
  const { t } = usePreferences();
  const cache = useQueryClient();
  const navigate = useNavigate();
  const [root, setRoot] = useState("");
  const [workspace, setWorkspace] = useState("");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const [unknownOutcome, setUnknownOutcome] = useState(false);
  const catalogue = useQuery({
    queryKey: ["workspaces"], queryFn: () => api.workspaces(),
    retry: false, staleTime: 10000,
  });
  async function refresh() {
    const result = await catalogue.refetch();
    if (result.isSuccess) { setUnknownOutcome(false); setError(""); }
    void cache.invalidateQueries({ queryKey: ["inventory"] });
  }
  async function change(action: "add" | "open", item: { root: string; workspace?: string }) {
    if (pending || unknownOutcome) return;
    setPending(true); setError("");
    try {
      if (action === "add") { await api.addWorkspace(item.root, item.workspace); setRoot(""); setWorkspace(""); }
      else {
        const session = await api.openWorkspace(item.root, item.workspace);
        if (session.session_id) navigate(workspacePath(session.session_id));
      }
      void cache.invalidateQueries({ queryKey: ["workspaces"] });
      void cache.invalidateQueries({ queryKey: ["inventory"] });
    } catch (e) {
      setError(actionableError(e));
      setUnknownOutcome(e instanceof ApiFailure && e.code === "outcome_unknown");
    } finally { setPending(false); }
  }
  async function pick() {
    setError("");
    try { const path = await desktop.pickWorkspace(); if (path) setRoot(path); }
    catch (e) { setError(actionableError(e)); }
  }
  return <Panel className="desktop-section workspace-catalogue" aria-label={t("Configured workspaces")}>
    <div><h2>{t("Configured workspaces")}</h2><p>{t("Add an existing configured folder to the catalogue. Open creates a session; services remain stopped until you explicitly start them.")}</p></div>
    <form className="workspace-catalogue-form" onSubmit={event => { event.preventDefault(); if (root.trim()) void change("add", { root: root.trim(), workspace: workspace.trim() || undefined }); }}>
      <label>{t("Workspace folder")}<Input value={root} disabled={pending || unknownOutcome} placeholder="/path/to/workspace" onChange={e => setRoot(e.target.value)} /></label>
      <label>{t("Configuration file (optional)")}<Input value={workspace} disabled={pending || unknownOutcome} placeholder="stackharbor.workspace.yaml" onChange={e => setWorkspace(e.target.value)} /></label>
      {isDesktop() ? <Button type="button" variant="outline" disabled={pending || unknownOutcome} onClick={() => void pick()}>{t("Choose folder…")}</Button> : null}
      <Button type="submit" disabled={!root.trim() || pending || unknownOutcome}>{t("Add workspace")}</Button>
      <Button type="button" variant="outline" disabled={pending || catalogue.isFetching} onClick={() => void refresh()}>{t("Refresh catalogue")}</Button>
    </form>
    {error ? <Notice title={t(unknownOutcome ? "Workspace action outcome unknown" : "Workspace action failed")} danger><p>{desktopMessage(t, error)}</p>{unknownOutcome ? <p>{t("Refresh the catalogue and inspect the current sessions before repeating this action.")}</p> : null}</Notice> : null}
    {catalogue.isPending ? <p role="status">{t("Loading configured workspaces…")}</p>
      : catalogue.error ? <Notice title={t("Workspace catalogue unavailable")}><p>{t(actionableError(catalogue.error))}</p><p>{t("Your live sessions remain available below.")}</p></Notice>
      : !catalogue.data?.workspaces?.length ? <p>{t("No configured workspaces have been added.")}</p>
      : <ul className="workspace-catalogue-list">{catalogue.data.workspaces.map(item => <WorkspaceItem key={`${item.namespace}:${item.root}`} item={item} disabled={pending || unknownOutcome} onOpen={() => void change("open", item)} />)}</ul>}
  </Panel>;
}
function WorkspaceItem({ item, disabled, onOpen }: { item: WorkspaceEntry; disabled: boolean; onOpen: () => void }) {
  const { t } = usePreferences();
  const uncertain = item.state === "recovery_required" || item.state === "unknown";
  return <li className="workspace-catalogue-item">
    <div className="workspace-catalogue-identity"><strong>{item.root}</strong><Badge variant="outline">{t(item.state)}</Badge></div>
    <p className="workspace-catalogue-meta">{t("Namespace: {namespace} · Configuration: {workspace}", { namespace: item.namespace, workspace: item.workspace || t("Auto-detected") })}</p>
    {item.issues?.length ? <ul>{item.issues.map((issue, i) => <li key={i}>{desktopMessage(t, issue)}</li>)}</ul> : null}
    {uncertain ? <Notice title={t("Explicit recovery required")}><p>{t("The previous process state could not be verified. Inspect and recover this workspace explicitly before opening it. StackHarbor will not adopt or restart its projects automatically.")}</p></Notice> : null}
    <div className="desktop-actions">{item.session?.session_id ? <Button variant="outline" asChild><Link to={workspacePath(item.session.session_id)}>{t("View session")}</Link></Button> : null}
      <Button variant="outline" disabled={disabled || uncertain || Boolean(item.session?.session_id)} onClick={onOpen}>{t("Open workspace")}</Button></div>
  </li>;
}
