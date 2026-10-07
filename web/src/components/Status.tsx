import { usePreferences } from "../lib/preferences";
import { Badge } from "./ui/badge";
export function Status({ value }: { value: string }) {
  const {t: tr} = usePreferences();
  const good = ["running", "ready", "succeeded", "Live", "session"].includes(
    value,
  );
  const danger = ["failed", "error", "unavailable"].includes(value);
  return (
    <Badge variant={danger ? "destructive" : good ? "default" : "secondary"}>
      {tr(value)}
    </Badge>
  );
}
