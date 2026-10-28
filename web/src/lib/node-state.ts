// Health and identity failures need attention even when a process still owns its ports.
// Keep the workbench incidents and workspace abnormal counts on the same contract.
export function isAbnormalNodeState(state: string) {
  return ["failed", "error", "blocked", "unready", "unavailable", "unhealthy", "unknown"].includes(state);
}
