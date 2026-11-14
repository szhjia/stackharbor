type Translator = (text: string, values?: Record<string, string | number>) => string;

// Native diagnostics contain paths and status values. Translate the bounded
// message templates without translating or altering their diagnostic values.
export function desktopMessage(t: Translator, message: string): string {
  const background = /^Background engine status: (.+)\. Review System Settings > General > Login Items & Extensions\.$/.exec(message);
  if (background) return t("Background engine status: {status}. Review System Settings > General > Login Items & Extensions.", { status: desktopMessage(t, background[1]) });
  const approval = /^Background service requires macOS authorization \((.+)\); approve it in Login Items & Extensions, then resume setup$/.exec(message);
  if (approval) return t("Background service requires macOS authorization ({status}); approve it in Login Items & Extensions, then resume setup", { status: desktopMessage(t, approval[1]) });
  const login = /^Login startup authorization is pending \((.+)\)$/.exec(message);
  if (login) return t("Login startup authorization is pending ({status})", { status: desktopMessage(t, login[1]) });
  for (const prefix of ["Original backup preserved", "Modified entry preserved", "Unowned integration was preserved", "Integration did not verify", "Unsafe integration ancestor", "Unsafe integration parent", "Unsafe file", "Unsafe shell profile"]) {
    if (message.startsWith(`${prefix}: `)) return t(`${prefix}: {path}`, { path: message.slice(prefix.length + 2) });
  }
  if (message.startsWith("error: ")) return t("Error: {detail}", { detail: t(message.slice(7)) });
  return t(message);
}
