import { useEffect } from "react";
import { toast } from "sonner";

const ids = {
  request: "console-request-error",
  results: "console-request-results",
  collection: "console-collection-error",
  partial: "console-partial-observation",
  inventory: "console-inventory-error",
};

export function useConsoleNotifications({
  error,
  results,
  collectionError,
  partial,
  inventoryError,
  tr,
  setError,
  setResults,
  navigate,
}: {
  error: string;
  results: string[];
  collectionError?: string;
  partial?: boolean;
  inventoryError: string;
  tr: (text: string) => string;
  setError: (error: string) => void;
  setResults: (results: string[]) => void;
  navigate: (path: string) => void;
}) {
  useEffect(() => {
    if (!error) { toast.dismiss(ids.request); return; }
    toast.error(tr("Request requires attention"), {id: ids.request, description: error,
      onDismiss: () => setError(""), onAutoClose: () => setError("")});
  }, [error, tr, setError]);
  useEffect(() => {
    if (!results.length) { toast.dismiss(ids.results); return; }
    toast(tr("Per-session requests"), {id: ids.results, description: results.join(" · "),
      action: {label: tr("Inspect operations"), onClick: () => navigate("/operations")},
      onDismiss: () => setResults([]), onAutoClose: () => setResults([])});
  }, [results, tr, navigate, setResults]);
  useEffect(() => {
    if (!collectionError) { toast.dismiss(ids.collection); return; }
    toast.error(tr("Inventory discovery unavailable"), {id: ids.collection,
      description: collectionError + tr(". Retained records and totals may be incomplete.")});
  }, [collectionError, tr]);
  useEffect(() => {
    if (!partial) { toast.dismiss(ids.partial); return; }
    toast.warning(tr("Partial observation"), {id: ids.partial,
      description: tr("Some sessions or metrics are unavailable. Retained observations may be stale.")});
  }, [partial, tr]);
  useEffect(() => {
    if (!inventoryError) { toast.dismiss(ids.inventory); return; }
    toast.warning(tr("Inventory unavailable"), {id: ids.inventory,
      description: inventoryError + tr(". Retrying discovery.")});
  }, [inventoryError, tr]);
}
