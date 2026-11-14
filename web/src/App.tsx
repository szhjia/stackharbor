import { BrowserRouter, useLocation, useRoutes } from "react-router";
import { lazy, Suspense, useState } from "react";
import { ConsoleToaster } from "./components/ConsoleToaster";
import { PreferencesProvider, usePreferences } from "./lib/preferences";
import { AppShell } from "./layout/AppShell";
import { consoleRoutes } from "./routes";
import { Skeleton } from "./components/ui/skeleton";
import { DesktopGate, SetupPreview } from "./desktop/Setup";
import { DesktopRouteState, restoreDesktopRoute } from "./desktop/route-state";

const SettingsPage = lazy(() => import("./pages/SettingsPage").then(module => ({default: module.SettingsPage})));
const WorkbenchPage = lazy(() => import("./pages/Workbench").then(module => ({default: module.WorkbenchPage})));
const WorkspacesPage = lazy(() => import("./pages/ConsolePages").then(module => ({default: module.WorkspacesPage})));
const WorkspacePage = lazy(() => import("./pages/ConsolePages").then(module => ({default: module.WorkspacePage})));
const ResourcesPage = lazy(() => import("./pages/ConsolePages").then(module => ({default: module.ResourcesPage})));
const OperationsPage = lazy(() => import("./pages/ConsolePages").then(module => ({default: module.OperationsPage})));
const NotFoundPage = lazy(() => import("./pages/ConsolePages").then(module => ({default: module.NotFoundPage})));

export function App() {
  useState(() => { restoreDesktopRoute(); return true; });
  return <PreferencesProvider><ConsoleToaster /><BrowserRouter><DesktopRouteState /><ApplicationRoutes /></BrowserRouter></PreferencesProvider>;
}
function ApplicationRoutes() {
  const route = useLocation();
  return route.pathname === "/desktop/setup-preview" ? <SetupPreview /> : <DesktopGate><AppRoutes /></DesktopGate>;
}
function RouteFallback() {
  const {t: tr} = usePreferences();
  return <section className="route-page" aria-label={tr("Loading page…")}>
    <Skeleton className="h-20 w-full" />
    <p role="status">{tr("Loading page…")}</p>
  </section>;
}
function AppRoutes() {
  const pages = {
    Settings: <SettingsPage />,
    Workbench: <WorkbenchPage />,
    Workspaces: <WorkspacesPage />,
    "Runtime resources": <ResourcesPage />,
    Operations: <OperationsPage />,
    "Page not found": <NotFoundPage />,
  };
  return useRoutes([{
    element: <AppShell />,
    children: consoleRoutes.map((route) => ({
      ...route,
      element: <Suspense fallback={<RouteFallback />}>
        {route.path.includes(":sessionID") ? <WorkspacePage /> : pages[route.handle.page as keyof typeof pages]}
      </Suspense>,
    })),
  }]);
}
