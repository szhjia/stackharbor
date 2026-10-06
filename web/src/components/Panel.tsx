import type { ComponentProps } from "react";
import { cn } from "../lib/utils";

export function Panel({as: Component = "div", radius = "default", className, ...props}: ComponentProps<"div"> & {
  as?: "div" | "section";
  radius?: "default" | "compact";
}) {
  return <Component className={cn("panel", radius === "compact" && "panel-compact", className)} {...props} />;
}
