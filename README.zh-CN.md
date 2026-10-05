# StackHarbor · 栈港

**给不断增长的本地应用，找一个统一停靠的港湾。**

[English](README.md) · [发行版本](https://github.com/szhjia/stackharbor/releases) · [Agent 技能](skills/stackharbor/SKILL.md) · [参与贡献](CONTRIBUTING.md)

[![检查](https://github.com/szhjia/stackharbor/actions/workflows/check.yml/badge.svg)](https://github.com/szhjia/stackharbor/actions/workflows/check.yml)
[![版本](https://img.shields.io/github/v/release/szhjia/stackharbor)](https://github.com/szhjia/stackharbor/releases/latest)
[![MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![skills.sh](https://skills.sh/b/szhjia/stackharbor)](https://skills.sh/szhjia/stackharbor/stackharbor)

## 为什么做这个项目

随着各种 AI 编程工具的爆发式发展，把一个想法变成应用越来越容易。我可以很快做出一个能用的应用，然后开始下一个。但每多一个应用，就多一套启动命令、端口、后台进程、日志和依赖。应用越来越多，记住它们怎么启动、哪些还在运行，也逐渐变成了一份额外的工作。

我做 StackHarbor 的初衷，就是减少这部分管理负担。在工作区根目录打开终端，把各个工程放到同一个视图里，需要时启动、查看、停止。让时间更多地花在创造和使用应用上，少一点花在寻找终端标签页和回忆启动步骤上。

## 开发哲学

- **让越来越多的应用仍然容易管理。** 一眼看清谁在运行、日志在哪里、哪里需要处理。
- **尊重工程已有的工作方式。** 用可读的 YAML 注册实际前台命令，保留各工程自己的语言、包管理器和启动契约。
- **让操作保持显式。** 打开控制台不自动启动服务；停止和重启依赖前，先展示影响范围。
- **尊重不同对象的生命周期。** 持久数据库、一次性迁移和常驻 Web 服务分别建模为资源、任务和服务。
- **如实展示状态。** 不知道的指标显示未知，明确区分本会话管理的进程和外部已经运行的服务。
- **让人和 AI 共享同一份契约。** 可版本管理的配置、只读计划和随工具维护的技能，让接入过程可检查、可复现。
- **保持专注，立足本地。** 用一个终端程序、有限的日志和清理预算解决问题，避免再增加一套托管控制平台。

## 界面释义：以 Harbor Café 为例

[Harbor Café](examples/harbor-cafe) 是专门为本说明编写、可以实际运行的咖啡店案例：**Menu API** 提供三款饮品，**Order counter** 从 API 读取菜单。点单服务必须等菜单 API 就绪后才能启动。运行 `make demo`，再按 **Shift+S**。

![macOS Terminal 中实际运行的 Harbor Café 总览](docs/screenshots/harbor-cafe-dashboard.png)

这是 macOS 自带 Terminal 的真实运行截图。左侧列出两个工程；纵向分割线让没有侧栏背景色的终端也能清楚区分导航和内容。**Session running 2/2** 表示本会话启动的两个服务都在运行；**Port owner** 表示监听端口的归属，内存和 CPU 来自实际进程观测。**Session events** 展示菜单 API 就绪后才启动点单服务的顺序。

![macOS Terminal 中点单服务的实际日志](docs/screenshots/harbor-cafe-logs.png)

按 **↓** 选中 **Order counter**，即可阅读实时日志。访问 `http://127.0.0.1:18282/` 会从 API 读取饮品，并产生截图中的请求日志。按 **i** 展开命令和路径详情，**o** 打开服务页面，**Home** 回到总览。按 **q** 停止这两个由本会话管理的进程并退出。

软件界面使用英文，本说明也有 [English 版本](README.md#interface-walkthrough-harbor-café)。对于使用 Docker 的工作区，上下选择 Docker，左右切换容器；`d` 保留为可选快捷入口。这个咖啡店案例不需要 Docker，当前不统计容器 CPU 和内存。

## 在 macOS 或 Linux 安装

发行包支持 Apple Silicon / Intel Mac，以及 arm64 / amd64 Linux。运行二进制不需要 Go。下载并查看安装脚本后，安装最新稳定版：

```sh
curl -fsSL https://raw.githubusercontent.com/szhjia/stackharbor/main/scripts/install-release.sh -o /tmp/stackharbor-install.sh
sh /tmp/stackharbor-install.sh
export PATH="$HOME/.local/bin:$PATH"
stackharbor --version
```

安装器识别平台，使用发行版的 SHA256 清单验证下载包，默认安装到 `~/.local/bin`，不使用 sudo，也不会覆盖已有命令。需要时将 PATH 配置加入 shell 配置文件。指定版本和安装目录：

```sh
sh /tmp/stackharbor-install.sh 0.1.0 "$HOME/.local/bin"
```

也可以从 [Releases](https://github.com/szhjia/stackharbor/releases) 下载压缩包和 `SHA256SUMS`，校验、解压后，在解压目录执行 `sh scripts/install.sh`。升级前核实已有命令，把旧二进制移到备份位置再安装，保留回退能力。发行包尚未经过 Apple 公证。

交互模式需要真实终端。工具用 `ps` 观察进程树，安装 `lsof` 后可识别实际监听者；缺失时对应观测显示未知。Linux 打开网址需要 `xdg-open`。被管理的应用仍需自己的运行依赖；仅使用 Compose 资源时需要 Docker。

## 一键编译并启动

安装 Go 1.26+、Git 和 Make 后：

```sh
git clone https://github.com/szhjia/stackharbor.git
cd stackharbor
make demo
```

这条命令编译工具并打开自带的 Harbor Café 咖啡店演示。按 **Shift+S** 启动服务，默认使用 18281–18282 端口；出现端口冲突时先核实影响。按 `q` 停止本会话启动的进程并退出。

| 命令 | 用途 |
| --- | --- |
| `make build` | 编译到 `dist/stackharbor` |
| `make run ARGS="--root /path/to/workspace"` | 编译并打开指定工作区 |
| `make demo` | 编译并打开可独立运行的演示 |
| `make install` | 编译并安装到 `~/.local/bin` |
| `make check` | 格式、vet、测试、竞态检测 |
| `make release VERSION=0.1.0` | 打包四个平台发行包 |

没有 Make 时，执行 `go build -o dist/stackharbor ./cmd/stackharbor`，再运行 `./dist/stackharbor --root /path/to/workspace`。

## 接入自己的应用

在工作区根目录运行 `stackharbor`，工具自动发现 `stackharbor.yaml`，并读取已有的 `stackharbor.workspace.yaml`。从 manifest 发现但尚未注册的候选只供检查，不会自动执行。

确认应用实际使用 `pnpm dev` 并监听 3000 端口后，在应用目录放置：

```yaml
version: 2
project: {id: web, name: Web}
context: {cwd: .}
services:
  dev:
    run: {command: [pnpm, dev]}
    ports: [{name: http, port: 3000}]
    ready: {tcp: "127.0.0.1:3000"}
    open: "http://127.0.0.1:3000/"
```

```sh
stackharbor discover --json
stackharbor validate --json
stackharbor plan start --target web/service/dev --json
stackharbor
```

YAML 中的端口用于描述和观测，不会改变应用监听配置。命令采用 argv 数组，路径相对注册文件解析，且必须位于目标工作区内。v2 将资源、任务和服务分开并按依赖编排，同时兼容 v1。详见 [v2 协议](docs/protocol-v2.md)、[v1 英文参考](skills/stackharbor/references/registration.md) 和 [详细使用说明](docs/usage.zh-CN.md)。

## 配合 AI 编程工具使用

通过 skills CLI 安装技能（需要 Node.js/npm）：

```sh
npx skills add szhjia/stackharbor
```

公开技能页面：[skills.sh 上的 stackharbor](https://skills.sh/szhjia/stackharbor/stackharbor)。上述命令直接从本 GitHub 仓库安装技能。

然后告诉 Agent：

> 使用 stackharbor 技能，在我的 Mac 上安装 StackHarbor，并按当前工程真实的启动方式完成接入。

技能包含 GitHub 发行版安装、工程发现、YAML 注册、声明校验与只读计划。安装技能本身不会安装应用。也可在永久保留的源码或发行解压目录执行 `sh scripts/install-skill.sh`，将完整技能链接到 `~/.agents/skills`。

使用 Docker 的应用启动前，需要分别检查 `docker compose version`、`docker info` 和依赖容器健康。技能已加入前置检查；用户要求启动本地应用时，可先启动已安装的 Docker Desktop 并等待引擎就绪。StackHarbor 程序本身不会自动打开 Docker Desktop。详见 [Docker 前置检查](skills/stackharbor/references/docker.md)。

## 快捷键

| 按键 | 操作 |
| --- | --- |
| ↑/↓ 或 j/k；Home | 选择工程或 Docker；返回总览 |
| ←/→ 或 h/l | 选择服务、任务、容器或日志范围 |
| s / x / r | 启动 / 停止 / 重启当前对象 |
| Shift+S / Shift+X / Shift+R | 全部启动 / 全部停止 / 重启原运行集合 |
| d | 快速进入 Docker（也可上下选择） |
| i / o | 展开详情 / 打开服务网址 |
| PageUp / PageDown / End | 日志历史或总览翻页 / 最新 |
| Tab | 窄终端切换侧栏与正文 |
| ? / Esc | 帮助 / 关闭覆盖层 |
| q / Ctrl-C | 停止本会话进程后退出 |

停止或重启受影响的依赖需要确认。持久资源单独控制；退出不会自动停止 Docker 容器或删除数据。

## 能力边界

StackHarbor 以当前用户权限运行本地前台进程，不提供安全沙箱。常规退出只清理本会话进程，共享最多 30 秒清理预算。外部端口冲突须明确确认后才能释放，发送信号前重新核对进程身份；不支持无法观察的脱离式 daemon。

就绪探针仅访问环回地址。日志每服务最多 2,000 行 / 2 MiB，总计 16 MiB，并清除终端控制序列。RSS 可能重复计算共享页，进程 CPU 可超过 100%；外部服务只读观测。`NO_COLOR` 禁用颜色，`STACKHARBOR_CACHE_DIR` 可指定会话锁与历史缓存目录。

当前早期版本不提供 Windows、网页控制台、后台托管、自动重启、热加载或容器资源统计。配置校验成功不代表已经验证应用的真实启动和数据库迁移行为。

## 贡献与安全

详见 [贡献指南](CONTRIBUTING.md)、[安全政策](SECURITY.md) 和 [变更记录](CHANGELOG.md)。CI 检查 macOS 和 Linux；版本标签通过 GitHub Actions 发布压缩包及校验清单。

相关项目：[Process Compose](https://github.com/F1bonacc1/process-compose)、[Overmind](https://github.com/DarthSim/overmind)。StackHarbor 聚焦工程发现、自注册和工作区总览。

## Star 曲线

[![Star History Chart](https://api.star-history.com/svg?repos=szhjia/stackharbor&type=Date)](https://www.star-history.com/#szhjia/stackharbor&Date)

曲线由 Star History 根据公开 GitHub 数据生成，新活动可能需要一段时间才会反映到图表中。

## 许可证

[MIT](LICENSE)。第三方许可证见 [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES) 和 `licenses/`。
