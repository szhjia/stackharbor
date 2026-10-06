# StackHarbor

V0.2.0：在 monorepo 根目录运行的 macOS/Linux 终端服务控制台。

应用内置的界面、帮助、提示及诊断统一使用英文。协议中的用户自定义展示名称仍可使用中文或其他语言，服务日志保留原文。

左侧为工作台与子工程导航，名称和状态分行显示；工作台用服务表格展示实际观测状态、端口归属及资源指标，Running 包含就绪的外部服务，Session 单独统计本会话管理的服务，工程页以日志为主，按 i 展开路径、命令和监听者详情。页面统一保留左右各 2 个字符、上下各 1 行的内边距，标题、侧栏、正文、帮助和快捷栏共用同一边界。侧栏内部左右各留 2 格、上下各留 1 行，名称与状态相邻，项目之间留 1 行空白；背景和文字随终端 light/dark 模式自动适配，背景按终端单元格覆盖完整列宽，正文、帮助页和分隔线使用终端默认背景。底部单行工具栏按“名称 快捷键”排列，名称与按键间隔 1 格，操作组之间间隔 3 格（窄屏 2 格），只提示当前页面的操作，宽终端右侧显示工具资源。子工程用 YAML 自注册，v2 显式区分资源、任务和服务，按依赖顺序导航及编排；进入工具不会自动启动服务。

## 使用

安装一次后，在任意项目根目录运行：

```sh
stackharbor
```

工具自动以当前目录为工程根，读取根目录的 `stackharbor.workspace.yaml`（若存在），发现各子工程。无需每次传入路径或 workspace 参数；进入工具后选择服务启动。

首次安装：解压对应系统/架构的发行包，在解压目录执行 `sh scripts/install.sh`。默认安装到 `~/.local/bin`，此目录需要位于 PATH；也可传入已有的 PATH 目录。安装器不会覆盖已有命令。源码目录同样支持此命令，先构建二进制即可。

构建要求 Go 1.26+。运行已构建二进制无需 Go；被管理的工程仍需要自己的运行依赖。以下命令用于构建、检查或显式选择其他工程：

```sh
go build -o dist/stackharbor ./cmd/stackharbor
./dist/stackharbor --version
./dist/stackharbor discover --root /path/to/monorepo --json
./dist/stackharbor validate --root /path/to/monorepo
./dist/stackharbor --root /path/to/monorepo
```

交互模式需要真实终端。macOS/Linux 使用系统 `ps` 观察进程树；安装 `lsof` 可显示实际监听与归属，缺失时端口观测显示未知，启动前仍做环回地址绑定检查。Linux 打开网址需要 `xdg-open`。

### 找回已有会话

不同工作区可以同时运行，同一个真实路径保持一个管理会话。再次打开已有工作区时，会显示路径、PID、TTY 和可观测的终端类型，并尝试直接切换到 macOS Terminal 的对应标签页；成功定位返回 0，无法定位返回 1 并保留定位提示。

```sh
stackharbor sessions                 # 列出仍在运行的会话
stackharbor sessions --json          # JSON 列表
stackharbor sessions --root /path/to/workspace
stackharbor sessions --focus 12345   # 将 PID 对应的 Terminal 标签页切到前台
```

`sessions` 不需要在工程目录运行，也不需要交互终端或读取工程配置。只显示仍持有系统锁的会话，退出后保留的锁文件不会被当成活动会话。旧版本的空锁文件会尝试用 `lsof` 和 `ps` 恢复所属 PID、TTY；当进程工作目录与锁身份一致时，也能恢复工程路径。无法观测的信息显示 `—`。

窗口切换目前支持 macOS 自带 Terminal，按 TTY 精确选择标签页并恢复最小化的窗口。其他终端、tmux/screen、SSH 和 Linux 仍可查看列表。macOS 可能要求允许终端自动化；没有权限或找不到标签页时会报告原因。定位不会启动或停止服务。同一工作区的会话与列表命令需要使用相同的 `STACKHARBOR_CACHE_DIR`。

### 关闭本工程的会话

在工程目录运行 `stackharbor kill`，无需寻找窗口或输入 PID。命令按当前目录及 workspace 配置确定真实工作区路径，仅关闭该工作区的已有会话；其它工程保持运行。支持 `--root PATH` 和 `--workspace FILE` 指定工作区。

发送 SIGTERM 前核对 PID、进程创建时间、程序名称及工作区锁文件归属，由已有会话按正常退出流程清理服务和恢复终端。最多等待 35 秒；超时或无法核验归属时返回错误，不升级为强制杀死。没有活动会话时返回成功。自定义缓存目录仍需使用相同的 `STACKHARBOR_CACHE_DIR`；macOS 使用系统 `lsof` 核验锁文件归属，Linux 使用 `/proc`。

| 按键 | 操作 |
|---|---|
| ↑/↓ 或 j/k；Home | 选择工程；返回工作台 |
| ←/→ 或 h/l | 切换全部/单服务日志 |
| s / x / r | 启动 / 停止 / 重启当前工程或服务 |
| Shift+S / Shift+X / Shift+R | 全部启动 / 停止 / 重启；v2 重启原运行集合，持久容器单独控制（停止、重启需确认） |
| d | Docker 快捷入口；也可 ↑/↓ 选中 Docker，←/→ 选择容器，s/x/r 单独控制 |
| PageUp / PageDown / End | 日志历史 / 最新；工作台翻页 / 最后一页 |
| i | 展开或收起当前工程详情 |
| o；?；Esc | 打开网址；帮助；取消覆盖层 |
| Tab | 窄终端切换工程列表与内容 |
| q / Ctrl-C | 停止本会话进程后退出 |

停止、重启会连带处理运行中的下游，操作前列出影响并确认。退出和 SIGTERM 共用最多 30 秒清理预算；停止不完整会返回运行错误，禁止重复启动仍有受管进程的服务。

## YAML 注册

在子工程目录放置 `stackharbor.yaml`，一个工程可以定义多个服务。命令使用 argv 数组；端口声明用于观测和冲突检查，具体端口仍由应用自己的参数或环境配置。

```yaml
version: 1
project: {id: shop, name: Shop}
services:
  web:
    run: {command: [pnpm, dev]}
    ports: [{name: http, port: 3000}]
    ready: {tcp: "127.0.0.1:3000"}
    open: "http://127.0.0.1:3000/"
```

[v2 协议与迁移契约](protocol-v2.md)；[v1 兼容参考](../skills/stackharbor/references/registration.md)。可选 workspace 文件支持排除目录、显式导入和指定目标根。相对路径基于 YAML 所在目录；服务 cwd 必须在目标工程根内。

工具源码或发行目录用 `.stackharbor-tool` 标记，自动发现会跳过嵌套的工具目录和工具根下的演示目录，避免将演示 YAML 混入业务工程。显式 workspace 导入不受此标记影响。

自动发现已有注册，以及 JS `dev`/`start`、pnpm workspace 和 Python manifest 候选。候选先显示“待注册”，不能直接执行。生成草案：

```sh
stackharbor init --dry-run
stackharbor init --write
stackharbor validate --json
```

`--write` 只处理命令明确的候选，拒绝覆盖现有文件或符号链接。Python 模块与未知包管理器需要填写。`--project /path/to/candidate` 可限定候选。退出码：成功 0，配置/用法错误 2，运行错误 1。v1 JSON `schema_version: 1`；v2 输出 schema 2 的节点与依赖，不输出 env 字段值。新增 plan、doctor、task run、history，详见 v2 参考。

## 演示与本项目接入

通用演示只依赖 Go 标准库，复制目录后可独立运行；默认端口 18080–18082：

```sh
cp -R examples/minimal /tmp/stackharbor-demo
stackharbor --root /tmp/stackharbor-demo
cp -R examples/multi-service /tmp/stackharbor-multi
stackharbor --root /tmp/stackharbor-multi
```

快捷键字母表示动作，Shift 将范围扩大到全部：s/x/r 控制当前对象，Shift+S/X/R 控制全部。旧 a/A 绑定已移除。

在工作台按 Shift+S 启动全部，或选中工程后按 s。工作台的 s/x/r 不执行动作。第二个演示的前端依赖 API 就绪。若端口已有外部进程，启动或重启前会列出冲突端口、PID 和命令；按 y 确认释放后启动，Esc/n 取消。也可修改示例端口、ready、open 和 DEMO_PORT。

`examples/pagesmith/` 是需要按目标工程调整的参考配置；通用可运行演示使用 minimal 和 multi-service。

## 可复用 skill

使用 [stackharbor 技能](../skills/stackharbor/SKILL.md) 将其它工程规范化接入栈港。在本仓库根目录或永久保留的发行解压目录执行：

```sh
sh scripts/install-skill.sh
```

默认将完整技能目录软链接到 `~/.agents/skills/stackharbor`；开启新会话后，其它工程即可使用。可传入实际运行环境的技能目录，例如 `sh scripts/install-skill.sh "$HOME/.codex/skills"`。重复安装同一来源成功，已有其它内容不覆盖。

技能随工具提交及打包，源码更新会通过链接同步；请保留来源目录。二进制升级仍须单独进行。技能指导读取真实启动契约，新接入优先 v2，保留已有配置，执行声明校验和只读计划，最后给出交互启动命令。安装、升级和维护细节见 [维护参考](../skills/stackharbor/references/maintenance.md)。

## 执行与统计边界

首版使用本地前台进程：独立工作目录、环境与进程组，记录 PID 和创建时间并跟踪观察到的后代。服务仍具有当前用户权限，不提供文件系统、网络或权限安全沙箱。双重 fork 且无法观察父链的 daemon 不受支持。

常规停止和退出只清理本会话启动的进程。启动/重启遇到外部端口冲突时，可确认释放：先发 TERM，3 秒后仍未退出则发 KILL；每次发送信号前核对 PID 和创建时间，监听者变化则拒绝继续。Docker/虚拟机/SSH 转发进程需在对应入口停止。服务内存汇总观察到的进程常驻内存（RSS），工具内存单列；共享页可能重复计入。受管服务统计本会话进程树；已在外部启动的服务根据声明端口定位监听进程及其后代，显示只读资源来源，不改变进程所有权。Docker/虚拟机/SSH 转发进程不计作应用资源；若能在已配置 Compose 范围内按发布端口唯一关联运行容器，则显示该端点容器的 Docker 指标，不汇总其它 worker 容器。外部服务的就绪探针通过后显示 Running ext；没有探针仅显示 Listening，失败显示 Unready ext，端口探测未知显示 Unknown。未运行或无法观测的服务仍显示 `—`；内存和 CPU 的读取失败分别处理，互不隐藏有效结果。CPU 首次采样未知，后续为进程 CPU 时间增量之和，可超过 100%。采样失败保留部分小计；未知显示 `—`。

就绪探针只访问环回目标，HTTP 重定向也受限。v1 超时保留“未就绪”进程，恢复后允许等待中的依赖继续；v2 首次就绪超时会结束本次计划并阻塞后继，恢复后可显式重试。日志每服务最多 2000 行/2 MiB、总计 16 MiB、每行 16 KiB，丢弃旧日志会提示；终端控制序列会移除。

`NO_COLOR` 禁用界面颜色。`STACKHARBOR_CACHE_DIR` 可覆盖默认用户缓存锁目录；同一工程的所有会话需使用同一缓存目录。锁文件保持在缓存中，进程退出释放系统锁，无需删除文件。

## 开发与发行

```sh
go test -race ./... -count=1 -timeout 120s
go vet ./...
./scripts/release.sh
```

测试用临时监听和进程，不接管本机工程。发行脚本生成 macOS/Linux × arm64/amd64 的压缩包及 SHA256 校验表，含示例、skill、说明和第三方许可证。当前不包含 Windows、容器资源统计、后台 daemon、自动重启、热加载或网页控制台。

相关开源项目：[Process Compose](https://github.com/F1bonacc1/process-compose) 提供 YAML 进程编排、依赖和 TUI；[Overmind](https://github.com/DarthSim/overmind) 使用 Procfile 与 tmux。StackHarbor 将子工程自动发现、自注册和工程级 Dashboard 组合为当前工作流。

MIT，见 [LICENSE](../LICENSE)。


## Docker 依赖

自动读取工程根目录的 `compose.yaml` / `compose.yml` / `docker-compose.yaml` / `docker-compose.yml`（按此顺序选择第一个）。上下导航到 Docker（或按 `d`），左右切换容器；页面显示容器状态、健康和发布端口，支持单独控制；容器状态读取失败显示未知及原因。快捷键字母使用蓝色粗体，无独立背景。

容器内存与 CPU 随后台采样动态刷新：每 2 秒触发观察，按运行中容器的 ID 批量执行 `docker stats --no-stream --no-trunc --format '{{json .}}'`，实际间隔受命令耗时影响；界面每 100 毫秒读取最新快照。v2 采样注册的 Compose 资源，以及在同一配置范围内按 TCP 发布端口唯一关联的外部应用容器，指标同时显示在 Dashboard、基础设施详情和 Docker 页面。容器内存使用 Docker CLI 的内存用量（Linux 下已扣除缓存），与宿主进程 RSS 的统计口径不同；CPU 使用 Docker 返回的百分比，可超过 100%。零占用显示为 `0`；停止、缺失或采样失败显示 `—`，失败会给出原因并清除旧值，不改变独立读取的容器可用状态。

本地服务用 Compose 服务名声明依赖：

```yaml
services:
  dev:
    run:
      command: ["./run.sh", "local"]
    docker_depends_on: [db, redis]
```

本地服务启动前按 Compose 依赖顺序启动这些容器，等待运行或健康。全部启停只包含已注册的本地服务及声明的 Docker 依赖。单个容器启动使用 `up -d --no-deps --no-build --wait`，不会隐式运行其他容器。停止/重启 Docker 依赖时，先停止本会话依赖它的本地服务；重启成功后恢复原运行集合。Docker 操作不会删除容器数据或卷，退出 TUI 不自动停止 Docker。Docker 引擎须已运行，缺少镜像或 Compose 所需环境配置时会报告错误。

## 浏览器及会话控制

保持工作区 TUI 前台打开，在另一终端使用 `stackharbor web`；默认端口 16800，仅绑定 127.0.0.1。`--port 0 --no-open` 分配随机端口、打印一次性链接（60 秒有效）。关闭网关不关闭应用。认证过期或网关重启后，重新运行该命令、打开新链接。

`status --all --json` 聚合当前命名空间；`start/stop/restart --target NODE --root ROOT --dry-run` 获取真实计划，`--yes` 明确执行。`release --target NODE --port PORT` 只释放已声明冲突端口。`logs --target NODE --follow` 读取日志；`operations --id ID` 恢复断线、超时后的操作结果，避免重复执行。`kill --root ROOT --yes` 关闭会话，保留持久 Compose 资源和数据。

所有终端需要相同 `STACKHARBOR_CACHE_DIR`；过期数据、未知指标和旧版只读会话会明确显示。计划 60 秒后过期、节点或容器身份改变返回 409，需要重新确认；日志截断显示缺口。具体命令、认证、命名空间、资源边界和源码前端工具链见 [Web/CLI 完整指南](web-control.md)。
