import type { ReactNode } from "react";
import { cn } from "../lib/utils";

export interface DefinitionItem {
  label: string;
  value: ReactNode;
  mono?: boolean;
}
export function DefinitionList({items, layout = "details", label}: {
  items: DefinitionItem[];
  layout?: "details" | "grid" | "inline";
  label?: string;
}) {
  return <dl aria-label={label} className={cn(layout === "details" ? "definition-list" : `stat-list stat-list-${layout}`)}>
    {items.map(item => <div key={item.label}>
      <dt>{item.label}</dt>
      <dd className={item.mono ? "mono" : undefined}>{item.value}</dd>
    </div>)}
  </dl>;
}
