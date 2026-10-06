import type { ReactNode } from "react";

export function AppContent({children}: {children: ReactNode}) {
  return <div id="app-content" className="app-content" tabIndex={-1}>{children}</div>;
}
