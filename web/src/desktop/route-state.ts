import { useEffect } from "react";
import { useLocation } from "react-router";
import { desktopNamespace } from "./bridge";

function key(namespace: string) { return `stackharbor.desktop.route:${namespace}`; }
export function rememberDesktopRoute(path: string) {
  const namespace = desktopNamespace();
  if (!namespace) return;
  try { localStorage.setItem(key(namespace), path); } catch { /* Storage can be unavailable. */ }
}
export function restoreDesktopRoute() {
  const namespace = desktopNamespace();
  if (!namespace || location.pathname !== "/" || location.search || location.hash) return;
  try {
    const path = localStorage.getItem(key(namespace));
    if (path && path.length < 4096 && /^\/(?:workspaces(?:\/[^?#]*)?|resources|operations|settings)?(?:\?[^#]*)?$/.test(path))
      history.replaceState(history.state, "", path);
  } catch { /* A fresh window remains usable without stored navigation. */ }
}
export function DesktopRouteState() {
  const route = useLocation();
  useEffect(() => rememberDesktopRoute(route.pathname + route.search), [route.pathname, route.search]);
  return null;
}
