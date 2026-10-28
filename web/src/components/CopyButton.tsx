import { useState } from "react";
import { usePreferences } from "../lib/preferences";
import { Button } from "./ui/button";

export function CopyButton({value, label}: {value: string; label: string}) {
  const {t: tr} = usePreferences();
  const [status, setStatus] = useState("");
  return <span className="copy-control">
    <Button variant="outline" disabled={!value} onClick={async () => {
      try {
        await navigator.clipboard.writeText(value);
        setStatus("Copied");
      } catch { setStatus("Copy failed. Select and copy the text manually."); }
    }}>{tr(label)}</Button>
    {status ? <span className="caption" role="status">{tr(status)}</span> : null}
  </span>;
}
