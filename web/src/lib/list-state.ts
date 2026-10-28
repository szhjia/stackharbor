import { useSearchParams } from "react-router";
import type { Node } from "../api/types";

export function matchesSearch(text: string, search: string) {
  const normalize = (value: string) => value.normalize("NFKC").toLocaleLowerCase().trim();
  return normalize(text).includes(normalize(search));
}

// Keep filters in the address so Back, reload and deep links restore the same list.
export function useListQuery() {
  const [params, setParams] = useSearchParams();
  return {
    search: params.get("q") ?? "",
    sort: params.get("sort") ?? "default",
    kind: params.get("kind") ?? "all",
    update: (key: string, value: string) => setParams(previous => {
      const next = new URLSearchParams(previous);
      if (!value || (key === "sort" && value === "default") || (key === "kind" && value === "all")) next.delete(key);
      else next.set(key, value);
      return next;
    }, {replace: true}),
  };
}

export function nodeSearchText(node: Node, tr: (value: string) => string) {
  return [node.name, node.id, node.project_id, node.state, tr(node.state), node.reason,
    node.ownership, tr(node.ownership), ...node.depends_on,
    ...node.ports.flatMap(port => [port.port, port.status, tr(port.status), port.reason])].join(" ");
}
