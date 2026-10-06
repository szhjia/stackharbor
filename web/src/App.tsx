import { BrowserRouter, useRoutes } from "react-router";
import { AppShell } from "./layout/AppShell";
import { consoleRoutes } from "./routes";
import { OverviewPage, WorkspacesPage, WorkspacePage, ResourcesPage, OperationsPage, NotFoundPage } from "./pages/ConsolePages";

export function App() {
  return <BrowserRouter><AppRoutes /></BrowserRouter>;
}
function AppRoutes() {
  const pages = {
    Overview: <OverviewPage />,
    Workspaces: <WorkspacesPage />,
    Resources: <ResourcesPage />,
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
