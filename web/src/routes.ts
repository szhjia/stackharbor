export function workspacePath(sessionID: string, tab = "services", target = "") {
  const path = `/workspaces/${encodeURIComponent(sessionID)}${tab === "services" ? "" : "/" + tab}`;
  return target ? path + "?" + new URLSearchParams({target}) : path;
}

export const consoleRoutes = [
  {path: "/", handle: {page: "Overview", tab: "services"}},
  {path: "/workspaces", handle: {page: "Workspaces", tab: "services"}},
  {path: "/resources", handle: {page: "Resources", tab: "services"}},
  {path: "/settings", handle: {page: "Settings", tab: "services"}},
  {path: "/operations", handle: {page: "Operations", tab: "services"}},
  {path: "/workspaces/:sessionID", handle: {page: "Workspaces", tab: "services"}},
  ...["tasks", "resources", "logs"].map((tab) => ({path: `/workspaces/:sessionID/${tab}`, handle: {page: "Workspaces", tab}})),
  {path: "*", handle: {page: "Page not found", tab: "services"}},
];
