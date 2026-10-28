import { BrowserRouter, useRoutes } from "react-router";
import { PreferencesProvider } from "./lib/preferences";
import { SettingsPage } from "./pages/SettingsPage";
import { WorkbenchPage } from "./pages/Workbench";
import { AppShell } from "./layout/AppShell";
import { consoleRoutes } from "./routes";
import { WorkspacesPage, WorkspacePage, ResourcesPage, OperationsPage, NotFoundPage } from "./pages/ConsolePages";

export function App() {
  return <PreferencesProvider><BrowserRouter><AppRoutes /></BrowserRouter></PreferencesProvider>;
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
      element: route.path.includes(":sessionID") ? <WorkspacePage /> : pages[route.handle.page as keyof typeof pages],
    })),
  }]);
}
