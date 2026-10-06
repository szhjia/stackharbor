import type { ReactNode } from "react";
import { cn } from "../lib/utils";
import { Button } from "./ui/button";
import { HoverCard, HoverCardContent, HoverCardTrigger } from "./ui/hover-card";

// Interaction timings belong to this shared primitive, not individual entities.
const OPEN_DELAY = 250;
const CLOSE_DELAY = 150;
export function InfoPopover({title, label, summary, className, children}: {
  title: string;
  label: string;
  summary: ReactNode;
  className?: string;
  children: ReactNode;
}) {
  return <HoverCard openDelay={OPEN_DELAY} closeDelay={CLOSE_DELAY}>
    <HoverCardTrigger asChild>
      <Button variant="ghost" size="sm" className={cn("info-popover-trigger", className)} aria-label={label}>
        {summary}
      </Button>
    </HoverCardTrigger>
    <HoverCardContent align="start" className="info-popover-content">
      <strong>{title}</strong>
      {children}
    </HoverCardContent>
  </HoverCard>;
}
