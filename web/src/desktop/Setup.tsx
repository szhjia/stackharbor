import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { useLocation } from "react-router";
import { Panel } from "../components/Panel";
import { Notice } from "../components/Notice";
import { Button } from "../components/ui/button";
import { Dialog, DialogContent, DialogTitle, DialogDescription, DialogFooter } from "../components/ui/dialog";
import { usePreferences } from "../lib/preferences";
import { desktop, isDesktop, type DesktopAgreement, type SetupOptions, type SetupStatus } from "./bridge";
import { desktopMessage } from "./messages";
import "./desktop.css";

const errorText = (error: unknown) => error instanceof Error ? error.message : typeof error === "string" ? error : "Desktop integration could not be verified.";
const ready = (status: SetupStatus) => status.complete && status.agreement.accepted;
const DesktopContext = createContext<{ status: SetupStatus; update: (status: SetupStatus) => void } | undefined>(undefined);

export function DesktopGate({ children }: { children: ReactNode }) {
  const { t } = usePreferences();
  const [status, setStatus] = useState<SetupStatus>();
  const [error, setError] = useState("");
  async function reload() {
    setError("");
    try { setStatus(await desktop.status()); } catch (e) { setError(errorText(e)); }
  }
  useEffect(() => { if (isDesktop()) void reload(); }, []);
  if (!isDesktop()) return children;
  if (status && ready(status)) return <DesktopContext.Provider value={{ status, update: setStatus }}>{children}</DesktopContext.Provider>;
  return <main className="desktop-setup-page">
    {status ? <SetupPanel status={status} statusError={error} onComplete={setStatus} onRefresh={reload} />
      : <Panel className="desktop-section"><h1>{t("StackHarbor desktop setup")}</h1>
        {error ? <Notice title={t("Desktop status unavailable")} danger><p>{desktopMessage(t, error)}</p><Button onClick={() => void reload()}>{t("Check again")}</Button></Notice>
          : <p role="status">{t("Checking installed integrations…")}</p>}
      </Panel>}
  </main>;
}

function StatusDetails({ status }: { status: SetupStatus }) {
  const { t } = usePreferences();
  const loginState = [...new Set([t(status.autostart.enabled ? "Enabled" : "Off"), desktopMessage(t, status.autostart.status)])].join(" · ");
  const needsApproval = ["requiresApproval", "requires_approval"].includes(status.background.status)
    || ["requiresApproval", "requires_approval"].includes(status.autostart.status);
  return <div className="desktop-status">
    <dl className="desktop-facts">
      <dt>{t("Application")}</dt><dd>{status.installed_path || t("Not installed in Applications")} · {status.version === "preview" ? t("Preview") : status.version}</dd>
      <dt>{t("Command line")}</dt><dd><code>{status.cli_path}</code></dd>
      <dt>{t("Agent skill")}</dt><dd><code>{status.skill_path}</code></dd>
      <dt>{t("Shell PATH")}</dt><dd>{t(status.path_configured ? "Configured" : "Not configured")}</dd>
      <dt>{t("Background engine")}</dt><dd>{desktopMessage(t, status.background.status)}</dd>
      <dt>{t("Login startup")}</dt><dd>{loginState}</dd>
    </dl>
    {needsApproval ? <Notice title={t("Approval required")}><p>{t("Allow StackHarbor in System Settings → General → Login Items & Extensions, then check the status again.")}</p></Notice> : null}
    {["disabled", "not_registered", "notRegistered"].includes(status.background.status) ? <Notice title={t("Background engine unavailable")}><p>{t("Enable StackHarbor in System Settings → General → Login Items & Extensions. Use Repair integrations if registration is missing.")}</p></Notice> : null}
    {status.conflicts.length ? <Notice title={t("Existing files require attention")} danger><ul>{status.conflicts.map((conflict) => <li key={conflict.path}><code>{conflict.path}</code>: {desktopMessage(t, conflict.reason)}</li>)}</ul><p>{t("Conflicting entries are preserved unless you explicitly choose backed-up migration below.")}</p></Notice> : null}
    {status.issues.length ? <Notice title={t("Integration issues")} danger><ul>{status.issues.map((issue, i) => <li key={i}>{desktopMessage(t, issue)}</li>)}</ul></Notice> : null}
  </div>;
}

export function SetupPanel({ status, onComplete, onRefresh, preview = false, onCancel, statusError }: {
  status: SetupStatus;
  onComplete: (status: SetupStatus) => void;
  onRefresh: () => void;
  preview?: boolean;
  onCancel?: () => void;
  statusError?: string;
}) {
  const { t, language } = usePreferences();
  const [agreement, setAgreement] = useState<DesktopAgreement>();
  const [agreementAccepted, setAgreementAccepted] = useState(false);
  const [cliAccepted, setCliAccepted] = useState(false);
  const [skillAccepted, setSkillAccepted] = useState(false);
  const [autoStart, setAutoStart] = useState(status.autostart.enabled);
  const [configurePath, setConfigurePath] = useState(false);
  const [migrateConflicts, setMigrateConflicts] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [needsInspection, setNeedsInspection] = useState(false);
  useEffect(() => { setNeedsInspection(false); }, [status]);
  async function loadAgreement() {
    setError("");
    try { setAgreement(preview ? previewAgreement : await desktop.agreement()); }
    catch (e) { setError(errorText(e)); }
  }
  useEffect(() => { void loadAgreement(); }, [preview]);
  async function install() {
    if (!agreement || !agreementAccepted || !cliAccepted || !skillAccepted || preview || busy || needsInspection) return;
    setBusy(true);
    setError("");
    const options: SetupOptions = { acceptAgreement: true, agreementHash: agreement.hash, autoStart, configurePath, migrateConflicts };
    try { onComplete(await desktop.setup(options)); }
    catch (e) { setNeedsInspection(true); setError(errorText(e)); }
    finally { setBusy(false); }
  }
  return <Panel className="desktop-section desktop-setup" aria-label={t("Desktop setup")}>
    <div><h1>{t(status.complete ? "Repair desktop integrations" : "Set up StackHarbor")}</h1>
      <p>{t("StackHarbor installs its command line and agent skill in your user account. Opening a workspace does not start its services.")}</p></div>
    {preview ? <Notice title={t("Browser preview")}><p>{t("This page shows sample setup states. Installation and system actions are unavailable in a browser.")}</p></Notice> : null}
    <StatusDetails status={status} />
    {statusError ? <Notice title={t("Status could not be refreshed")} danger><p>{desktopMessage(t, statusError)}</p><p>{t("The displayed status is the last verified result.")}</p></Notice> : null}
    {agreement ? <>
      <div><h2>{t("Installation agreement")}</h2><p>{preview ? t("StackHarbor · sample data") : agreement.publisher} · {t("Version {version}", { version: preview ? t("Preview") : agreement.version })}</p>
        {language === "zh-CN" ? <>
          <pre className="desktop-agreement" tabIndex={0} aria-label={t("Installation agreement summary")}>{t("Desktop agreement summary", { publisher: preview ? t("StackHarbor · sample data") : agreement.publisher, cliPath: status.cli_path, skillPath: status.skill_path })}</pre>
          <details><summary>{t("View the complete original agreement")}</summary><pre className="desktop-agreement" tabIndex={0} aria-label={t("Full installation agreement")}>{agreement.text}</pre></details>
        </> : <pre className="desktop-agreement" tabIndex={0} aria-label={t("Full installation agreement")}>{agreement.text}</pre>}
        <p className="caption">{t("Agreement hash:")} <code>{agreement.hash}</code></p></div>
      <label className="desktop-check"><input type="checkbox" checked={agreementAccepted} disabled={busy} onChange={e => setAgreementAccepted(e.target.checked)} />{t("I accept this version of the installation agreement.")}</label>
      <label className="desktop-check"><input type="checkbox" checked={cliAccepted} disabled={busy} onChange={e => setCliAccepted(e.target.checked)} /><span>{t("Install the required CLI at")} <code>{status.cli_path}</code>.</span></label>
      <label className="desktop-check"><input type="checkbox" checked={skillAccepted} disabled={busy} onChange={e => setSkillAccepted(e.target.checked)} /><span>{t("Install the required skill at")} <code>{status.skill_path}</code> {t("and supported Codex / Claude compatibility links.")}</span></label>
      <label className="desktop-check"><input type="checkbox" checked={configurePath} disabled={busy} onChange={e => setConfigurePath(e.target.checked)} />{t("Add the reversible managed PATH block shown in the agreement to the detected shell configuration.")}</label>
      <label className="desktop-check"><input type="checkbox" checked={autoStart} disabled={busy} onChange={e => setAutoStart(e.target.checked)} />{t("Start StackHarbor at login (optional). Projects are never started automatically.")}</label>
      {status.conflicts.length ? <label className="desktop-check"><input type="checkbox" checked={migrateConflicts} disabled={busy} onChange={e => setMigrateConflicts(e.target.checked)} />{t("Back up and migrate the conflicting entries listed above.")}</label> : null}
    </> : <Button variant="outline" disabled={busy} onClick={() => void loadAgreement()}>{t("Load full agreement")}</Button>}
    {error ? <Notice title={t("Setup needs attention")} danger><p>{desktopMessage(t, error)} {t("Check the actual status before trying setup again.")}</p></Notice> : null}
    <div className="desktop-actions">
      <Button disabled={preview || busy || needsInspection || !agreement || !agreementAccepted || !cliAccepted || !skillAccepted} onClick={() => void install()}>{t(busy ? "Verifying integrations…" : "Install and verify integrations")}</Button>
      <Button variant="outline" disabled={preview || busy} onClick={onRefresh}>{t("Check actual status")}</Button>
      {onCancel ? <Button variant="ghost" disabled={busy} onClick={onCancel}>{t("Cancel repair")}</Button> : null}
    </div>
  </Panel>;
}

export function DesktopSettings() {
  const { t } = usePreferences();
  const context = useContext(DesktopContext);
  const [repair, setRepair] = useState(false);
  const [uninstall, setUninstall] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [needsInspection, setNeedsInspection] = useState(false);
  if (!context) return null;
  const { status, update } = context;
  async function run(action: () => Promise<SetupStatus>, observation = false) {
    if (busy || (needsInspection && !observation)) return;
    setBusy(true); setError("");
    try { update(await action()); setUninstall(false); setNeedsInspection(false); }
    catch (e) { setNeedsInspection(true); setError(errorText(e)); }
    finally { setBusy(false); }
  }
  return <section className="desktop-maintenance" aria-label={t("Desktop integrations")}>
    <h2>{t("Desktop integrations")}</h2>
    <Panel className="desktop-section">
      <StatusDetails status={status} />
      {error && !uninstall ? <Notice title={t("Desktop action needs attention")} danger><p>{desktopMessage(t, error)} {t("Check the actual status before repeating this action.")}</p></Notice> : null}
      <div className="desktop-actions">
        <Button variant="outline" disabled={busy || needsInspection} onClick={() => setRepair(true)}>{t("Repair integrations")}</Button>
        <Button variant="outline" disabled={busy || needsInspection} onClick={() => void run(() => desktop.autostart(!status.autostart.enabled))}>{t(status.autostart.enabled ? "Disable login startup" : "Enable login startup")}</Button>
        <Button variant="outline" disabled={busy} onClick={() => void run(desktop.status, true)}>{t("Check actual status")}</Button>
        <Button variant="outline" disabled={busy || needsInspection} onClick={() => setUninstall(true)}>{t("Uninstall integrations…")}</Button>
      </div>
    </Panel>
    {repair ? <SetupPanel status={status} onComplete={next => { update(next); if (ready(next)) setRepair(false); }} onRefresh={() => void run(desktop.status, true)} onCancel={() => setRepair(false)} /> : null}
    {uninstall ? <Dialog open onOpenChange={open => { if (!open && !busy) setUninstall(false); }}><DialogContent showCloseButton={!busy}>
      <DialogTitle>{t("Remove StackHarbor integrations?")}</DialogTitle>
      <DialogDescription>{t("Stop your projects explicitly before uninstalling. This action removes only owned, unmodified CLI, skill and shell integrations. Your workspace YAML and user history are preserved.")}</DialogDescription>
      <p>{t("Uninstalling does not stop projects. The engine refuses removal while projects are running.")}</p>
      {error ? <Notice title={t("Desktop action needs attention")} danger><p>{desktopMessage(t, error)} {t("Check the actual status before repeating this action.")}</p><Button variant="outline" disabled={busy} onClick={() => void run(desktop.status, true)}>{t("Check actual status")}</Button></Notice> : null}
      <DialogFooter><Button disabled={busy || needsInspection} variant="destructive" onClick={() => void run(desktop.uninstall)}>{t("Remove owned integrations")}</Button><Button variant="outline" disabled={busy} onClick={() => setUninstall(false)}>{t("Cancel")}</Button></DialogFooter>
    </DialogContent></Dialog> : null}
  </section>;
}

const previewAgreement: DesktopAgreement = {
  version: "preview", hash: "preview-only-not-an-installation-agreement", publisher: "StackHarbor · sample data",
  text: "PREVIEW ONLY\n\nThe real desktop app loads the complete versioned agreement from its installed bundle before any external writes.\n\nRequired CLI: ~/.local/bin/stackharbor\nRequired canonical skill: ~/.agents/skills/stackharbor\nCompatibility links are disclosed before installation.\n\nOptional shell configuration preview:\n# >>> StackHarbor managed PATH >>>\nexport PATH=\"$HOME/.local/bin:$PATH\"\n# <<< StackHarbor managed PATH <<<\n\nThe real agreement includes detected shell paths, ownership and backup rules, publisher and notices.",
};
export function SetupPreview() {
  const route = useLocation();
  const { t } = usePreferences();
  const requested = new URLSearchParams(route.search).get("state") ?? "new";
  const state = ["new", "conflict", "approval", "disabled", "complete"].includes(requested) ? requested : "new";
  const previewStates = { new: "New installation", conflict: "Existing files", approval: "Approval pending", disabled: "Background disabled", complete: "Setup complete" };
  const status: SetupStatus = {
    complete: state === "complete", version: "preview", bundle_id: "com.xahacktech.stackharbor", installed_path: "/Applications/StackHarbor.app",
    cli_path: "~/.local/bin/stackharbor", skill_path: "~/.agents/skills/stackharbor", path_configured: state === "complete",
    agreement: { version: "preview", hash: previewAgreement.hash, accepted: state === "complete" },
    autostart: { enabled: false, status: "off" }, background: { status: state === "approval" ? "requiresApproval" : state === "disabled" ? "disabled" : "enabled" },
    conflicts: state === "conflict" ? [{ path: "~/.local/bin/stackharbor", reason: "An existing unmanaged file would be preserved." }] : [], issues: [],
  };
  return <main className="desktop-setup-page"><p>{t("Settings")} · {t("Browser preview")}</p><nav className="desktop-actions" aria-label={t("Preview states")}>{Object.entries(previewStates).map(([value, label]) => <a href={`?state=${value}`} key={value}>{t(label)}</a>)}</nav><SetupPanel key={state} status={status} preview onComplete={() => {}} onRefresh={() => {}} /></main>;
}
