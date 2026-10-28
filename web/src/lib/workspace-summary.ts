import type {Node} from "../api/types";
import {isAbnormalNodeState} from "./node-state";

export function summarizeWorkspaceNodes(nodes: Node[] | undefined) {
  if (!nodes) return undefined;
  const summary = {services: 0, tasks: 0, runningServices: 0, abnormal: 0};
  for (const node of nodes) {
    if (node.kind === "task") summary.tasks++;
    else {
      summary.services++;
      if (node.state === "running") summary.runningServices++;
    }
    if (isAbnormalNodeState(node.state)) summary.abnormal++;
  }
  return summary;
}
