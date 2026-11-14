/** Only the new macOS bundle injects this marker. Legacy Tauri uses HTTP. */
declare global {
  interface Window {
    __STACKHARBOR_DESKTOP__?: { namespace: string };
  }
}

export function desktopNamespace(): string | undefined {
  const marker = window.__STACKHARBOR_DESKTOP__;
  return marker && typeof marker.namespace === "string" && marker.namespace.length > 0
    ? marker.namespace : undefined;
}
export const isDesktop = () => desktopNamespace() !== undefined;

export async function nativeInvoke<T>(command: string, args?: Record<string, unknown>): Promise<T> {
  if (!isDesktop()) throw new Error("This action requires the installed StackHarbor desktop app.");
  const { invoke } = await import("@tauri-apps/api/core");
  return invoke<T>(command, args);
}

export function withDeadline<T>(promise: Promise<T>, milliseconds = 11000): Promise<T> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error("Local request timed out")), milliseconds);
    promise.then(resolve, reject).finally(() => clearTimeout(timer));
  });
}

export interface DesktopAgreement {
  version: string;
  hash: string;
  text: string;
  publisher: string;
}
export interface SetupStatus {
  complete: boolean;
  version: string;
  bundle_id: string;
  installed_path: string;
  cli_path: string;
  skill_path: string;
  path_configured: boolean;
  agreement: { version: string; hash: string; accepted: boolean };
  autostart: { enabled: boolean; status: string };
  background: { status: string };
  conflicts: { path: string; reason: string }[];
  issues: string[];
}
export interface SetupOptions {
  acceptAgreement: boolean;
  agreementHash: string;
  autoStart: boolean;
  migrateConflicts: boolean;
  configurePath: boolean;
}
export const desktop = {
  status: () => withDeadline(nativeInvoke<SetupStatus>("desktop_status")),
  agreement: () => withDeadline(nativeInvoke<DesktopAgreement>("desktop_agreement")),
  setup: (options: SetupOptions) => withDeadline(nativeInvoke<SetupStatus>("desktop_setup", { ...options }), 60000),
  autostart: (enabled: boolean) => withDeadline(nativeInvoke<SetupStatus>("desktop_set_autostart", { enabled })),
  pickWorkspace: () => nativeInvoke<string | null>("desktop_pick_workspace"),
  uninstall: () => withDeadline(nativeInvoke<SetupStatus>("desktop_uninstall"), 60000),
};
