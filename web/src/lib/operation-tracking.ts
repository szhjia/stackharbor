export interface TrackedOperation { session_id: string; id: string }
const key = "stackharbor.tracked-operations";
const limit = 100;

// This tab retains lookup identities, never cached outcomes or approval tokens.
export function readTrackedOperations(): TrackedOperation[] {
  try {
    const saved: unknown = JSON.parse(sessionStorage.getItem(key) ?? "[]");
    if (!Array.isArray(saved)) return [];
    const valid = saved.slice(-limit).filter((item): item is TrackedOperation =>
      item != null && typeof item === "object" && typeof item.session_id === "string" && Boolean(item.session_id)
      && typeof item.id === "string" && Boolean(item.id));
    return [...new Map(valid.map(item => [JSON.stringify([item.session_id, item.id]), {session_id: item.session_id, id: item.id}])).values()];
  } catch { return []; }
}

export function rememberTrackedOperations(operations: TrackedOperation[]) {
  try { sessionStorage.setItem(key, JSON.stringify(operations.slice(-limit))); }
  catch { /* Current-tab tracking still works when storage is unavailable. */ }
}
