import { Toaster } from "sonner";
import { usePreferences } from "../lib/preferences";

export function ConsoleToaster() {
  const {resolvedTheme, t: tr} = usePreferences();
  return <Toaster theme={resolvedTheme} position="bottom-right" closeButton containerAriaLabel={tr("Notifications")} />;
}
