# StackHarbor · 栈港

**一个轻量、本地、对开发者和 Coding Agent 友好的开发控制台。**

看清运行中的服务与端口，无需记忆各个应用的启动命令，明确控制启动与停止。

[English](README.md) · [发行版本](https://github.com/szhjia/stackharbor/releases) · [Agent 技能](skills/stackharbor/SKILL.md) · [参与贡献](CONTRIBUTING.md)

[![检查](https://github.com/szhjia/stackharbor/actions/workflows/check.yml/badge.svg)](https://github.com/szhjia/stackharbor/actions/workflows/check.yml)
[![版本](https://img.shields.io/github/v/release/szhjia/stackharbor)](https://github.com/szhjia/stackharbor/releases/latest)
[![MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![skills.sh](https://skills.sh/b/szhjia/stackharbor)](https://skills.sh/szhjia/stackharbor/stackharbor)

## 开发与设计哲学

AI 编程工具让想法变成应用越来越容易，但每多一个应用，就多一套启动命令、端口、日志和依赖需要管理。StackHarbor 把不断增长的本地应用集合放到同一个终端视图里，让开发者少花时间回忆命令、寻找终端标签页。

- **让日常开发保持轻量。** 用一个本地终端程序和可读配置管理应用，无需搭建托管控制平台。
- **沿用工程已有的工作方式。** 注册应用实际使用的前台命令，保留各工程自己的语言、包管理器和启动契约。
- **让运行状态清楚可见。** 展示观测到的服务、监听端口、端口归属和日志；未知指标显示未知，明确区分本会话进程与外部服务。
- **让操作保持显式。** 打开控制台不自动启动服务；停止和重启依赖前展示影响范围，释放外部端口前核对进程身份。
- **尊重不同对象的生命周期。** 持久数据库、一次性任务和常驻服务分别处理；常规退出清理本会话进程，保留持久资源。
- **让人和 Agent 共享同一份契约。** 可版本管理的 YAML、机器可读的发现与校验、只读计划和随工具维护的技能，让工程接入可检查、可复现。

## 核心优势

- **在一个视图里看清服务与端口。** 知道哪些已注册应用正在运行、谁占用了它们的监听端口、日志在哪里。端口声明描述预期监听信息，实际端口归属和就绪观测提供运行证据。
- **配置一次，日常无需记忆启动命令。** 把工作目录、命令、端口和依赖保存在 YAML 中，通过控制台启动、停止和重启，减少在不同应用之间回忆命令的负担。
- **把多个工程放进同一个工作区。** 发现各工程的注册文件并统一展示，同时保留各自的启动方式。尚未注册的候选只供检查，不会自动执行。
- **方便 Coding Agent 接入与检查。** 随工具提供的技能帮助 Agent 按真实启动命令安装、注册应用；JSON 发现、校验和只读依赖计划，让 Agent 在操作前有一致的配置检查入口。
- **本地运行，操作边界明确。** 控制台运行在本机，启动服务需要显式操作；外部服务只读观测，不因此获得停止权限，依赖变更先展示影响。通过这些控制减少操作误判；命令仍以当前用户权限执行。

## 与同类工具的不同

如果你的主要需求是管理不断增长的本地应用，随时看清哪些服务和端口在运行、各应用如何启动、哪些进程属于当前会话，StackHarbor 就是为这个场景设计的。它围绕已有命令、工程注册、工作区总览和显式生命周期控制，提供轻量的日常操作方式。

| 工具 | 主要侧重 | StackHarbor 的侧重与选择理由 |
| --- | --- | --- |
| [Process Compose](https://github.com/F1bonacc1/process-compose) | 通用本地进程编排，包含依赖、健康检查、自动恢复、终端界面、REST API 和 MCP 接口 | 聚焦工程发现、可复用注册、服务与端口可见性，以及日常开发中的显式会话控制 |
| [dekit（mprocs 的下一代）](https://github.com/pvolok/dekit) | 面向开发和生产的进程管理，支持依赖、崩溃恢复、后台运行，以及供人和 Agent 使用的 CLI | 聚焦前台本地控制台，常规退出清理会话拥有的进程并保留持久资源 |
| [Overmind](https://github.com/DarthSim/overmind) | 基于 Procfile 和 tmux 管理进程，并支持连接进程进行交互 | 使用 YAML 工程注册和工作区总览，无需依赖 tmux |
| [Tilt](https://github.com/tilt-dev/tilt) | 围绕代码变化、容器镜像构建及 Kubernetes 或 Compose 环境更新的开发循环 | 直接使用现有本地前台命令，按需接入 Compose 资源 |

StackHarbor 的轻量体现在接入所需组件少：一个终端二进制，无需 tmux，仅在应用使用 Compose 资源时需要 Docker。对于这个场景，优势是工程发现、服务与端口可见性、可复用命令和明确的会话归属共同带来的便利。可以按自己的工作方式选择；同类工具也提供本地运行和 Agent 支持。

## 安装

### 在 macOS 或 Linux 安装发行版

发行包支持 Apple Silicon / Intel Mac，以及 arm64 / amd64 Linux。运行二进制不需要 Go。下载并查看安装脚本后，安装最新稳定版：

```sh
curl -fsSL https://raw.githubusercontent.com/szhjia/stackharbor/main/scripts/install-release.sh -o /tmp/stackharbor-install.sh
sh /tmp/stackharbor-install.sh
export PATH="$HOME/.local/bin:$PATH"
stackharbor --version
```

安装器识别平台，使用发行版的 SHA256 清单验证下载包，默认安装到 `~/.local/bin`，不使用 sudo，也不会覆盖已有命令。需要时将 PATH 配置加入 shell 配置文件。指定版本和安装目录：

```sh
sh /tmp/stackharbor-install.sh 0.2.0 "$HOME/.local/bin"
```

也可以从 [Releases](https://github.com/szhjia/stackharbor/releases) 下载压缩包和 `SHA256SUMS`，校验、解压后，在解压目录执行 `sh scripts/install.sh`。升级前核实已有命令，把旧二进制移到备份位置再安装，保留回退能力。发行包尚未经过 Apple 公证。

交互模式需要真实终端。工具用 `ps` 观察进程树，安装 `lsof` 后可识别实际监听者；缺失时对应观测显示未知。Linux 打开网址需要 `xdg-open`。被管理的应用仍需自己的运行依赖；仅使用 Compose 资源时需要 Docker。

### 可选：安装 Coding Agent 技能

通过 skills CLI 安装技能（需要 Node.js/npm）：

```sh
npx skills add szhjia/stackharbor
```

公开技能页面：[skills.sh 上的 stackharbor](https://skills.sh/szhjia/stackharbor/stackharbor)。上述命令直接从本 GitHub 仓库安装技能。

安装技能本身不会安装 StackHarbor。也可在永久保留的源码或发行解压目录执行 `sh scripts/install-skill.sh`，将完整技能链接到 `~/.agents/skills`。

## 使用

### 体验自带演示

安装 Go 1.26+、Node 26.9.0/npm、Git 和 Make 后：

```sh
git clone https://github.com/szhjia/stackharbor.git
cd stackharbor
make demo
```

这条命令编译工具并打开 Harbor Café。按 **Shift+S** 启动服务，默认使用 18281–18282 端口；出现端口冲突时先核实影响。按 **q** 停止本会话启动的进程并退出。

### 界面释义：以 Harbor Café 为例

[Harbor Café](examples/harbor-cafe) 是专门为本说明编写、可以实际运行的咖啡店案例：**Menu API** 提供三款饮品，**Order counter** 从 API 读取菜单。点单服务必须等菜单 API 就绪后才能启动。运行 `make demo`，再按 **Shift+S**。

![macOS Terminal 中实际运行的 Harbor Café 总览](docs/screenshots/harbor-cafe-dashboard.png)

总览回答日常最常见的问题：哪些服务正在运行、监听哪些端口、端口属于谁。截图为旧版；当前 **Running 2/2** 包含就绪的外部服务，**Session 2** 单独统计本会话管理的服务。**Running ext** 表示外部端点就绪，**Listening** 只确认存在监听；**Port owner** 表示观测到的监听端口归属，内存和 CPU 来自实际进程观测。**Session events** 展示菜单 API 就绪后才启动点单服务的顺序。这是 macOS 自带 Terminal 的真实运行截图。

![macOS Terminal 中点单服务的实际日志](docs/screenshots/harbor-cafe-logs.png)

按 **↓** 选中 **Order counter**，即可阅读实时日志。访问 `http://127.0.0.1:18282/` 会从 API 读取饮品，并产生截图中的请求日志。按 **i** 展开命令和路径详情，**o** 打开服务页面，**Home** 回到总览。按 **q** 停止这两个由本会话管理的进程并退出。

软件界面使用英文，本说明也有 [English 版本](README.md#interface-walkthrough-harbor-café)。对于使用 Docker 的工作区，上下选择 Docker，左右切换容器；`d` 保留为可选快捷入口。容器页面动态显示内存和 CPU；这个咖啡店案例不需要 Docker。

### 接入自己的应用

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

在工作区根目录检查注册、校验配置、预览依赖计划，然后打开控制台：

```sh
stackharbor discover --json
stackharbor validate --json
stackharbor plan start --target web/service/dev --json
stackharbor
```

YAML 中的端口用于描述和观测，不会改变应用监听配置。命令采用 argv 数组，路径相对注册文件解析，且必须位于目标工作区内。v2 将资源、任务和服务分开并按依赖编排，同时兼容 v1。详见 [v2 协议](docs/protocol-v2.md)、[v1 英文参考](skills/stackharbor/references/registration.md) 和 [详细使用说明](docs/usage.zh-CN.md)。

接入后，按 **s / x / r** 启动、停止或重启当前服务，按 **Shift+S** 启动默认启动集合中的应用。打开控制台本身不会启动服务。应用启动命令或端口改变时，同步更新注册文件。

### 找回与关闭工作区会话

不同工作区可以同时打开。同一工作区再次运行时，会显示已有会话的路径、PID 和终端，并尝试定位到 macOS Terminal 中对应的标签页。窗口太多时可用 `stackharbor sessions` 查看所有活动会话，或用 `stackharbor sessions --focus PID` 找回窗口；`--json` 输出机器可读列表。其他终端及 Linux 显示可观测的 PID/TTY，暂不支持自动切换窗口。

在工程目录执行 `stackharbor kill` 可关闭本工作区的已有会话，清理它启动的服务；其它工程不受影响。也可用 `stackharbor kill --root /path/to/workspace` 指定工作区。

### 配合 AI 编程工具使用

安装配套技能后，告诉 Agent：

> 使用 stackharbor 技能，在我的 Mac 上安装 StackHarbor，并按当前工程真实的启动方式完成接入。

技能包含 GitHub 发行版安装、工程发现、YAML 注册、声明校验与只读计划。开发者与 Agent 复用同一份已注册启动命令。

使用 Docker 的应用启动前，需要分别检查 `docker compose version`、`docker info` 和依赖容器健康。技能已加入前置检查；用户要求启动本地应用时，可先启动已安装的 Docker Desktop 并等待引擎就绪。StackHarbor 程序本身不会自动打开 Docker Desktop。详见 [Docker 前置检查](skills/stackharbor/references/docker.md)。

### 快捷键

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

Docker 资源指标通过批量 `docker stats` 采样动态刷新。注册的 Compose 资源在总览、工程详情和 Docker 页面显示内存、CPU；外部转发端口在已配置 Compose 范围内唯一匹配运行容器时，工程行也显示该容器指标；采样失败显示未知值和原因。指标口径见 [使用说明](docs/usage.zh-CN.md#docker-依赖)。

当前早期版本不提供 Windows、后台托管、自动重启或热加载。配置校验成功不代表已经验证应用的真实启动和数据库迁移行为。

## 开发与贡献

从源码构建需要 Go 1.26+、Node 26.9.0/npm 和 Make：

```sh
make build
make run ARGS="--root /path/to/workspace"
```

| 命令 | 用途 |
| --- | --- |
| `make build` | 编译到 `dist/stackharbor` |
| `make run ARGS="--root /path/to/workspace"` | 编译并打开指定工作区 |
| `make demo` | 编译并打开可独立运行的演示 |
| `make install` | 编译并安装到 `~/.local/bin` |
| `make check` | 格式、vet、测试、竞态检测 |
| `make release VERSION=0.2.0` | 打包四个平台发行包与当前版本发布说明 |

没有 Make 时，先执行 `sh scripts/web-build.sh`，再执行 `go build -o dist/stackharbor ./cmd/stackharbor`，然后运行 `./dist/stackharbor --root /path/to/workspace`。

详见 [贡献指南](CONTRIBUTING.md)、[安全政策](SECURITY.md) 和 [变更记录](CHANGELOG.md)。CI 检查 macOS 和 Linux；版本标签通过 GitHub Actions 发布压缩包及校验清单。

## Star 曲线

[![Star History Chart](https://api.star-history.com/svg?repos=szhjia/stackharbor&type=Date)](https://www.star-history.com/#szhjia/stackharbor&Date)

曲线由 Star History 根据公开 GitHub 数据生成，新活动可能需要一段时间才会反映到图表中。

## 许可证

[MIT](LICENSE)。第三方许可证见 [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES) 和 `licenses/`。

## 浏览器与现有会话 CLI 控制

在工作区终端保持 StackHarbor 前台运行，再于另一个终端运行 `stackharbor web`。默认仅监听 `127.0.0.1:16800`；`--port 0 --no-open` 分配空闲端口并打印一次性启动链接。停止网关保留应用。`status/start/stop/restart/release/logs/operations/kill` 直接访问会话 Unix socket，不依赖 Web。非交互写操作需要 `--yes`；`--dry-run` 查看真实会话计划。认证过期后再次运行 `stackharbor web`，使用新链接。详见 [Web 与 CLI 控制](docs/web-control.md)。

源码构建新增 Node 26.9.0（`.node-version`）与 npm 要求；`make build/check/release` 安装锁定依赖、构建并核验真实前端。发行二进制包含 UI，运行不需要 Node 或 Go。原始 `go build` 不能证明已有前端产物与源码一致，请使用 Make 路径。
