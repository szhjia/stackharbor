import { usePreferences } from "../lib/preferences";
import type { ReactNode } from "react";
import { Link } from "react-router";
import { Panel } from "./Panel";
import { DefinitionList, type DefinitionItem } from "./DefinitionList";

export function SummaryCard({id, title, href, total, unit, items, description}: {
  id: string;
  title: string;
  href: string;
  total: ReactNode;
  unit: ReactNode;
  items: DefinitionItem[];
  description: ReactNode;
}) {
  const {t: tr} = usePreferences();
  return <Panel as="section" className="overview-summary" aria-labelledby={id}>
    <div className="section-heading">
      <h2 id={id}>{title}</h2>
      <Link className="text-primary" to={href}>{tr("View all →")}</Link>
    </div>
    <p className="overview-summary-total"><strong>{total}</strong> <span>{unit}</span></p>
    <DefinitionList layout="grid" items={items} />
    <p className="caption">{description}</p>
  </Panel>;
}
