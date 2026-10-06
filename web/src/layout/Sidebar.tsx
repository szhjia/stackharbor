import { Link } from "react-router";
import { Layers, Network, Activity, LayoutDashboard } from "lucide-react";
import brandMark from "../assets/stackharbor-mark.png";
import { NavigationMenu, NavigationMenuList, NavigationMenuItem, NavigationMenuLink } from "../components/ui/navigation-menu";
const navigation = [
  {name: "Overview", path: "/", icon: LayoutDashboard},
  {name: "Workspaces", path: "/workspaces", icon: Layers},
  {name: "Resources", path: "/resources", icon: Network},
  {name: "Operations", path: "/operations", icon: Activity},
];
export function Sidebar({page, sessionCount, collapsed}: {page: string; sessionCount: number; collapsed: boolean}) {
  return (
      <aside id="app-sidebar" className="sidebar">
        <header className="sidebar-header">
        <Link to="/" className="brand" aria-label="StackHarbor home" title={collapsed ? "StackHarbor" : undefined}>
          <img src={brandMark} alt="" />
          <span>
            StackHarbor<small>LOCAL CONTROL</small>
          </span>
        </Link>
        </header>
        <NavigationMenu viewport={false} className="console-nav">
          <NavigationMenuList>
            {navigation.map((item) => (
              <NavigationMenuItem key={item.name}>
                <NavigationMenuLink asChild>
                  <Link
                    to={item.path}
                    aria-label={item.name}
                    title={collapsed ? item.name : undefined}
                    aria-current={page === item.name ? "page" : undefined}
                  >
                    <item.icon />
                    <span>{item.name}</span>
                    {item.name === "Workspaces" ? (
                      <small>{sessionCount}</small>
                    ) : null}
                  </Link>
                </NavigationMenuLink>
              </NavigationMenuItem>
            ))}
          </NavigationMenuList>
        </NavigationMenu>
      </aside>
  );
}
