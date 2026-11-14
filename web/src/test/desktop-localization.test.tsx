import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { PreferencesProvider, usePreferences } from "../lib/preferences";
import { DesktopGate, DesktopSettings, SetupPreview } from "../desktop/Setup";
import { WorkspaceCatalogue } from "../desktop/WorkspaceCatalogue";
import { desktop, type SetupStatus } from "../desktop/bridge";
import { api } from "../api/client";
import { Status } from "../components/Status";

const status: SetupStatus = {
  complete: true, version: "0.9.2", bundle_id: "com.xahacktech.stackharbor",
  installed_path: "/Applications/StackHarbor.app", cli_path: "/home/user/.local/bin/stackharbor",
  skill_path: "/home/user/.agents/skills/stackharbor", path_configured: true,
  agreement: { version: "1", hash: "canonical-hash", accepted: true },
  autostart: { enabled: true, status: "enabled" }, background: { status: "enabled" }, conflicts: [], issues: [],
};

beforeEach(() => {
  localStorage.setItem("stackharbor.language", "zh-CN");
  window.__STACKHARBOR_DESKTOP__ = { namespace: "test" };
  vi.spyOn(desktop, "status").mockResolvedValue(status);
  vi.spyOn(desktop, "agreement").mockResolvedValue({ version: "1", hash: "canonical-hash", publisher: "Publisher", text: "Full canonical agreement." });
});
afterEach(() => { localStorage.clear(); delete window.__STACKHARBOR_DESKTOP__; });

function LanguageSwitch() {
  const { setLanguage } = usePreferences();
  return <><button onClick={() => setLanguage("en")}>English</button><button onClick={() => setLanguage("zh-CN")}>中文</button></>;
}

it("localizes preview metadata and preserved-file warnings without enabling installation", async () => {
  render(<PreferencesProvider><MemoryRouter initialEntries={["/desktop/setup-preview?state=conflict"]}><SetupPreview /></MemoryRouter></PreferencesProvider>);
  expect(await screen.findByText(/现有文件不由本应用管理，将予以保留。/)).toBeVisible();
  expect(await screen.findByText("StackHarbor · 示例数据 · 版本 预览")).toBeVisible();
  expect(screen.getByLabelText("安装协议中文说明")).not.toHaveTextContent("sample data");
  expect(screen.getByRole("button", { name: "安装并验证集成" })).toBeDisabled();
});

it("localizes installed desktop fields, actions and paused state and switches languages without repeating setup", async () => {
  const setup = vi.spyOn(desktop, "setup");
  render(<PreferencesProvider><DesktopGate><DesktopSettings /><Status value="Paused" /><LanguageSwitch /></DesktopGate></PreferencesProvider>);
  expect(await screen.findByRole("heading", { name: "桌面集成" })).toBeVisible();
  expect(screen.getByText("后台引擎")).toBeVisible();
  expect(screen.getByText("登录启动")).toBeVisible();
  expect(screen.getByRole("button", { name: "修复安装集成" })).toBeVisible();
  expect(screen.getByRole("button", { name: "关闭登录启动" })).toBeVisible();
  expect(screen.getByText("已暂停")).toBeVisible();
  expect(screen.queryByText("enabled", { exact: true })).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "English" }));
  expect(screen.getByRole("heading", { name: "Desktop integrations" })).toBeVisible();
  expect(screen.getByText("Paused")).toBeVisible();
  await userEvent.click(screen.getByRole("button", { name: "中文" }));
  expect(screen.getByRole("heading", { name: "桌面集成" })).toBeVisible();
  expect(desktop.status).toHaveBeenCalledTimes(1);
  expect(setup).not.toHaveBeenCalled();
});

it("localizes approval and setup copy while retaining the complete canonical agreement and its hash", async () => {
  vi.mocked(desktop.status).mockResolvedValue({ ...status, complete: false, background: { status: "requires_approval" }, agreement: { ...status.agreement, accepted: false } });
  render(<PreferencesProvider><DesktopGate>Console</DesktopGate></PreferencesProvider>);
  expect(await screen.findByRole("heading", { name: "设置 StackHarbor" })).toBeVisible();
  expect(screen.getByText("需要系统授权")).toBeVisible();
  expect(await screen.findByRole("checkbox", { name: "我接受此版本的安装协议。" })).not.toBeChecked();
  expect(screen.getByRole("button", { name: "安装并验证集成" })).toBeDisabled();
  expect(screen.getByText("canonical-hash")).toBeVisible();
  expect(screen.getByLabelText("完整安装协议").textContent).toBe("Full canonical agreement.");
});

it("localizes uninstall confirmation and known running-project refusal without weakening its guard", async () => {
  const uninstall = vi.spyOn(desktop, "uninstall").mockRejectedValue(new Error("Stop all running projects and operations before upgrading or uninstalling. Existing projects and cached clients were preserved."));
  render(<PreferencesProvider><DesktopGate><DesktopSettings /></DesktopGate></PreferencesProvider>);
  await userEvent.click(await screen.findByRole("button", { name: "卸载安装集成…" }));
  const dialog = screen.getByRole("dialog", { name: "移除 StackHarbor 安装集成？" });
  await userEvent.click(within(dialog).getByRole("button", { name: "移除应用管理的集成" }));
  expect(await screen.findByText(/升级或卸载前，请先停止所有运行中的项目和操作/)).toBeVisible();
  expect(within(dialog).getByRole("button", { name: "移除应用管理的集成" })).toBeDisabled();
  expect(uninstall).toHaveBeenCalledTimes(1);
});

it("localizes catalogue actions, offline and recovery states and recovery guidance", async () => {
  vi.spyOn(api, "workspaces").mockResolvedValue({ workspaces: [
    { root: "/projects/offline", namespace: "personal", workspace: "stackharbor.yaml", state: "offline" },
    { root: "/projects/recover", namespace: "personal", workspace: "stackharbor.yaml", state: "recovery_required", issues: ["Previous engine ended without confirmed process cleanup. Review recovery before starting."] },
  ] });
  render(<PreferencesProvider><QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><MemoryRouter><WorkspaceCatalogue /></MemoryRouter></QueryClientProvider></PreferencesProvider>);
  expect(await screen.findByRole("heading", { name: "已配置工作区" })).toBeVisible();
  expect(screen.getByRole("textbox", { name: "工作区文件夹" })).toBeVisible();
  expect(screen.getByRole("button", { name: "选择文件夹…" })).toBeVisible();
  expect(screen.getByRole("button", { name: "刷新工作区目录" })).toBeVisible();
  expect(await screen.findByText("离线")).toBeVisible();
  const recovery = screen.getByText("/projects/recover").closest("li")!;
  expect(within(recovery).getByText("需要恢复")).toBeVisible();
  expect(within(recovery).getByRole("button", { name: "打开工作区" })).toBeDisabled();
  expect(within(recovery).getByText(/上次引擎退出时未确认进程已清理/)).toBeVisible();
  expect(screen.queryByText("recovery_required", { exact: true })).not.toBeInTheDocument();
});

it("shows a localized default when the engine omits an automatically discovered configuration", async () => {
  vi.spyOn(api, "workspaces").mockResolvedValue({ workspaces: [{ root: "/projects/default", namespace: "personal", state: "offline" }] });
  render(<PreferencesProvider><QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><MemoryRouter><WorkspaceCatalogue /></MemoryRouter></QueryClientProvider></PreferencesProvider>);
  expect(await screen.findByText("命名空间：personal · 配置文件：自动检测")).toBeVisible();
  expect(screen.queryByText(/\{workspace\}/)).not.toBeInTheDocument();
});

it("localizes actual native post-uninstall issues while preserving paths and status values", async () => {
  vi.mocked(desktop.status).mockResolvedValue({ ...status, complete: false, background: { status: "not_registered" }, issues: [
    "Background engine status: not_registered. Review System Settings > General > Login Items & Extensions.",
    "Original backup preserved: /Users/test/backups/original-cli",
  ], conflicts: [{ path: "/Users/test/.local/bin/stackharbor", reason: "Existing entry is unowned or has been modified; explicit backed-up migration is required" }] });
  render(<PreferencesProvider><DesktopGate>Console</DesktopGate></PreferencesProvider>);
  expect(await screen.findByText("后台引擎状态：未注册。请检查“系统设置 → 通用 → 登录项与扩展”。")).toBeVisible();
  expect(screen.getByText("原始备份已保留：/Users/test/backups/original-cli")).toBeVisible();
  expect(screen.getByText(/现有项目不属于本应用或已被修改；需明确选择备份并迁移/)).toBeVisible();
});
