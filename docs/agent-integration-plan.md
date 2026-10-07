# 面向编码 agent 的集成计划（MCP 优先，DSH 插件暂缓）

设计日期：2026-10-07。状态：**计划，尚未实现**。本文只记录要做什么、为什么这样做、验收标准和暂缓项，不包含实现。

本期范围是让本机编码 agent（DeepSeek Harness、Claude Code、Cursor、Codex 等）通过稳定契约观测 StackHarbor 工作区，并在明确启用写工具后操作已有会话。先交付只读 MCP 试点，再以真实客户端验证和共享资源检查为门槛交付写工具；**DSH 插件（bundle、UI 面板）明确列为二期且暂不实施**，理由与触发条件见第 8 节。

关联文档：[协议 v2](protocol-v2.md)、[Web 与 CLI 控制](web-control.md)、[详细使用说明](usage.zh-CN.md)、[开发与文档约定](development.md)、[agent skill](../skills/stackharbor/SKILL.md)。

## 1. 范围

### 1.1 目标

- agent 能用一次调用拿到工作区注册、校验结果、依赖图与运行态，不必自己拼 CLI 参数或解析人类可读输出。
- 服务端保证写操作经过"会话计划 → 提交"，提交绑定会话内冻结的计划；用户确认由经过验证的 MCP 宿主审批策略负责。没有可信审批机制时，不能声称服务端保证用户已确认。
- 观测保持 StackHarbor 现有口径：`Listening` 不等于 ready，`external` 不等于 session 所有，未知值保持未知，截断与游标缺口显式可见。
- 一份实现服务所有 MCP 客户端，不绑定任何单一 agent。

### 1.2 本期做

1. `stackharbor mcp`：stdio MCP server；先交付 7 个只读工具，写路径满足第 4.3 节门槛后再启用 2 个工具（第 4.2 节）。
2. 契约文档 `docs/mcp.md`：工具、参数、结构化返回与错误、CLI 退出码对照、最小客户端配置示例。
3. 测试、真实客户端试用与验收（第 5 节）。

### 1.3 本期不做

| 不做的事 | 原因 |
| --- | --- |
| DSH 插件 / DSH bundle / DSH UI 面板 | 明确暂缓，见第 8 节 |
| 由 agent 拉起新的工作区会话（headless session） | 交互模式要求真终端（`internal/cli/run.go:150`，实测无 TTY 时退出码 2）；需要 PTY 托管能力，与二期共享前置 |
| 把 `internal/control`、`internal/sessionapi` 变成公开 API | 它们是内部包；对外只暴露 MCP 工具与现有 CLI |
| 绕过会话计划的写路径（如"直接 start"） | 与 StackHarbor 现有计划语义冲突；用户确认另由宿主审批负责 |
| 自动重试未知结果的写操作 | 可能在计划过期后再次执行用户意图；应先查询 operation 并保留未决状态 |
| Windows 支持 | 现有产品边界如此 |
| 修改 TUI、Web 网关、注册协议的行为 | 本期只增加入口，不改变既有语义 |

## 2. 现状：已经可以复用的契约

这些能力已经存在并已实测，一期不需要重写业务逻辑。

| 能力 | 位置 | 对外形态 |
| --- | --- | --- |
| 工作区发现与候选 | [internal/cli/output.go](../internal/cli/output.go) | `discover --json` |
| 声明校验 | [internal/cli/run.go](../internal/cli/run.go) | `validate --json`（与 `discover` 共用输出路径） |
| 离线依赖计划 | [internal/graph/plan.go](../internal/graph/plan.go) | `plan start\|stop\|restart --json`，`schema_version: 2` |
| 会话控制 | [internal/cli/control.go](../internal/cli/control.go) | `status/logs/operations/start/stop/restart/release/kill` |
| 会话协议（Unix socket） | [internal/sessionapi](../internal/sessionapi) | `/v1/identity`、`/v1/snapshot`、`/v1/plans`、`/v1/operations`、`/v1/logs`，`ProtocolVersion = 1` |
| 计划/提交语义 | [internal/control/plans.go](../internal/control/plans.go)、[operations.go](../internal/control/operations.go) | `control.Plan`、`control.SubmitRequest` |
| Web 网关 | [internal/web/server.go](../internal/web/server.go) | `127.0.0.1:16800`，`/api/v1/inventory`、`/api/v1/events`（需 cookie + CSRF） |
| agent skill | [skills/stackharbor/SKILL.md](../skills/stackharbor/SKILL.md) | 0.4.0，供会读技能的 agent 使用 |

实测样例（2026-10-07，`dist/stackharbor` 0.6.0）：

- `discover --root examples/harbor-cafe --json` → `{"schema_version":2,"root":…,"nodes":[{"id":"menu/service/api","kind":"service","requires":…}],"diagnostics":[]}`
- `plan start --root examples/harbor-cafe --target menu/service/api --json` → `{"schema_version":2,"config_digest":"0954…","action":"start","targets":[…],"ordered_layers":[[…]],"edges":[],"actions":[…],"unresolved_observations":[]}`
- `status --root <无会话的工作区> --json` → **退出码 1，输出是人类可读句子**，不是 JSON（见缺口 G3）
- `discover --root <配置无效的工作区> --json` → 退出码 2，仍输出 `diagnostics`（含 `severity`、`code`、`file`、`field`、`line`、`column`）
- `stackharbor --root <路径> </dev/null` → 退出码 2，`Interactive mode requires a terminal; use stackharbor discover or validate.`

### 2.1 必须照抄的既有语义

来自 [web-control.md](web-control.md) 与 [internal/control](../internal/control)：

- 非交互写操作必须 `--yes`；`--dry-run` 只返回计划。
- `discover` / `validate` 在配置无效时**仍输出诊断 JSON**，退出码 2（实测；此时 `schema_version` 可能回落为 1）。
- 会话内计划：`id / session_id / action / targets / affected / expires_at / fingerprint / warnings`，**TTL 60 秒**，最多 1000 个未过期计划；状态或身份变化返回 409，需要重新计划；过期返回 410 语义（`plan_expired`）。
- 提交需要 `plan_id` + `idempotency_key`（格式 `plan_id:nonce`，≤256 字节）；同一 plan 用同一 key 重放返回同一 operation，用不同 key 返回 `plan_conflict`。
- operation 终态保留 24 小时、最多 1000 条；队列最多 100 条等待项；超时或断连**不代表**操作未执行，必须用 `operations --id` 查询。
- CLI 可从会话结束后的完成记录恢复已知 ID 的 operation；无会话时不能查询任意尚未完成或未知 ID 的操作。
- CLI 和 Web 提交前会通过 `inventory.SharedImpact` 检查其他工作区的活动使用者；会话协议的 `Submit` 本身不执行这项跨会话检查。
- 接受的 action 集合（协议层）：`start`、`stop`、`restart`、`release`、`close`、`all-*`、`docker-*`、`release-*`。CLI 只暴露其中一部分。
- 退出码：0 成功；1 运行时错误；2 非法输入/确认或冲突被拒；3 结果未决或已过期。
- 错误码 → 退出码：`unsupported_protocol`、`invalid_request`、`forbidden`、`identity_conflict`、`idempotency_conflict`、`plan_expired`、`plan_conflict`、`shared_consumers` → 2；`result_unknown`、`operation_expired` → 3；其余 → 1。
- 日志有界：每服务 2000 行 / 2 MiB，总计 16 MiB，终端控制序列被剥离；现有 `control.LogPage` 已提供 `gap`、`dropped`、`reset`、`next_cursor`。
- 观测口径：`Listening` 只说明存在监听；`Running ext` 表示外部端点就绪；`ownership` 区分 `session` 与 `external`；无测量值显示未知并给出原因。

## 3. 缺口

| 编号 | 缺口 | 后果 |
| --- | --- | --- |
| G1 | 没有 MCP 入口 | agent 虽可调用部分 CLI JSON 命令，仍需自行处理参数、进程退出码与会话错误 |
| G2 | 没有面向 MCP 客户端的契约文档 | 现有 JSON 字段、错误码与 CLI 退出码未形成 MCP 工具的输入、结果与错误契约 |
| G3 | 无会话时的错误不是结构化输出 | 实测 `status --json` 无会话时是人类可读句子 + 退出码 1；MCP 工具必须把它转成结构化错误并说明前置条件 |
| G4 | 会话协议已有日志缺口字段，MCP 仍需保留并解释它们 | agent 若只读取日志文本，可能把"截断"当成"没有更多日志" |
| G5 | 两类"计划"容易混淆 | 离线配置计划（`graph.Plan`）与会话内计划（`control.Plan`）是两个不同对象，工具命名与文档必须区分 |
| G6 | CLI 无法把"准备"和"提交"拆成两次调用 | `start --yes` 内部一次完成计划与提交，只有 `--dry-run` 能单独取计划；真正的两阶段要走会话协议 |
| G7 | 直接调用会话协议会绕过 CLI/Web 的跨工作区影响检查 | 停止或重启共享资源时，MCP 必须在提交前执行相同的 `SharedImpact` 阻断规则 |
| G8 | MCP 计划 ID 与幂等键不是用户批准凭据 | 宿主若不审批 `apply`，agent 可以自行计划并立即提交；服务端不能据此宣称经过人工确认 |

## 4. 一期方案：`stackharbor mcp`

### 4.1 进程模型

| 方案 | 说明 | 结论 |
| --- | --- | --- |
| A. 独立 stdio 进程（推荐） | agent 把 `stackharbor mcp` 作为子进程拉起；进程本身不需要 TTY，通过 `internal/sessionapi` 客户端连接**已存在**的会话，与 CLI 同一条路径 | 采用 |
| B. 由运行中的 TUI 会话内嵌 MCP transport | 可以顺带支持"启动会话"，但要在 TUI 里多开传输、处理生命周期与安全面 | 推迟 |
| C. 用 shell 调 CLI 包一层 | 最省事，但拿不到"准备/提交"分离（G6），错误只能解析文本（G3） | 只用于只读命令的兜底 |

推荐实现层：新增 `internal/mcp`，`stackharbor mcp --stdio` 作为新的 CLI 子命令，直接使用 `internal/discovery`、`internal/graph`、`internal/inventory`、`internal/sessionapi`。**不 exec 自己**；写工具仍须复用 CLI/Web 的跨工作区影响检查，不能把直连会话协议等同于完整写路径。

### 4.2 候选工具清单（7 个只读，2 个写路径工具）

| 工具 | 等价 CLI / 协议 | 读写 | 前置 | 返回要点 |
| --- | --- | --- | --- | --- |
| `stackharbor_discover` | `discover --json` | 只读 | 无 | 节点/候选/诊断；按 `schema_version` 分支（v2 为 `nodes`，v1 为 `projects`+`candidates`） |
| `stackharbor_validate` | `validate --json` | 只读 | 无 | 与 `discover` 同一输出路径（v2 工作区给 `nodes`，v1 给 `projects`+`candidates`）；校验失败要给出来源文件、字段、行列 |
| `stackharbor_plan` | `plan <start\|stop\|restart> --json` | 只读 | 无 | 离线配置计划：`config_digest`、`ordered_layers`、`edges`、`unresolved_observations` |
| `stackharbor_sessions` | `sessions --json`、`status --all --json` | 只读 | 无 | 会话清单、可达性（`Partial` 要显式返回） |
| `stackharbor_status` | `status --json` | 只读 | 需会话 | `control.Snapshot`：`identity`、`revision`、`observed_at`、`nodes[]`（含 `ports`、`ownership`、`allowed_actions`）、`processes[]`、`containers[]`、`diagnostics[]` |
| `stackharbor_logs` | `logs --after N --limit N` | 只读 | 需会话 | 分页游标；保留 `gap`、`dropped`、`reset`，明确这些字段的含义 |
| `stackharbor_session_plan` | 会话协议 `POST /v1/plans` | 准备写操作，不执行；会在会话内保存短期计划 | 需会话 + 显式启用写工具 | `control.Plan`：`id`、`expires_at`、`affected`、`fingerprint`、`warnings`，并附跨工作区影响 |
| `stackharbor_apply` | 会话协议 `POST /v1/operations` | 写 | 需会话 + 显式启用写工具 + 有效 `plan_id` | 提交前重新检查跨工作区影响；返回 `control.Operation`，缺计划凭据直接拒绝 |
| `stackharbor_operation` | `operations --id --json` | 只读 | 活跃会话，或已知 ID 对应的会话结束完成记录 | 终态与逐目标结果；会话不可达且无完成记录时标为未决，活跃会话明确拒绝未知 ID |

说明：先交付 `discover`、`validate`、`plan`、`sessions`、`status`、`logs`、`operation` 并用真实客户端试用，再决定写工具的发布。`stackharbor_session_plan` 与 `stackharbor_apply` 是写路径的两段，必须成对使用；离线配置计划与会话计划是两个不同名字（G5）。工具名以试用结果定稿。

### 4.3 两阶段写路径（关键设计）

1. MCP 默认只注册只读工具。只有用户显式启用写工具时才注册 `stackharbor_session_plan` 和 `stackharbor_apply`；启用开关本身不等于逐次批准。
   `session_plan` 会占用会话内计划容量，工具元数据不能把它标成严格只读。
2. `stackharbor_session_plan` 创建会话内短期计划，不执行目标动作；返回计划 id、`expires_at`、受影响节点、警告及 `inventory.SharedImpact` 的共享资源影响。存在阻断项时明确返回或标示，不能呈现为可安全执行。
3. 宿主负责在调用 `apply` 前向用户展示计划并执行逐次审批。MCP 协议不提供可由服务端验证的通用审批凭据；工具说明、标注、`plan_id` 和 `idempotency_key` 均不能证明用户已确认。未验证宿主审批行为的集成只宣称"计划绑定的写操作"，不宣称"强制人工确认"；没有审批能力的宿主使用现有交互 CLI 完成需人工确认的操作。
4. `stackharbor_apply` 必须携带 `plan_id` 与 `idempotency_key`，缺任一即拒绝，**不提供隐式计划**。提交前重新收集 `inventory.SharedImpact`，按现有 CLI/Web 规则阻断活动共享使用者，并显式保留部分覆盖告警；复用同一业务判定，避免 MCP 直连绕过检查。
5. 超时或断连后不自动重试提交。有 operation ID 时先查询该 ID；无 ID 时查询会话 operation 列表并尝试核对，仍无法确认则报告结果未决，不把断连当作失败。
6. 过期计划返回 `plan_expired` 并要求重新计划；状态变化返回 `plan_conflict`/409 语义，不静默重算。
7. `release` 只接受显式声明过的端口，且仍然要求计划与宿主审批。观测类节点（`control: observe`）与外部端点不提供写路径；`allowed_actions` 为空即拒绝。

### 4.4 兼容与版本

- 对外结果区分 MCP 工具契约版本与既有发现/依赖计划的 `schema_version`；遇到不认识的业务版本，返回原始 JSON 与明确提示，不猜测字段。
- 会话 `ProtocolVersion != 1` 时报 `unsupported_protocol`，不尝试兼容。
- 一期兼容承诺：JSON 字段只增不改、不删；错误码集合只增。
- 契约文档 `docs/mcp.md`（本期交付物，尚未创建）与 `skills/stackharbor/references/` 的更新必须同步（见 [CONTRIBUTING](../CONTRIBUTING.md) 对协议变更的要求）。

### 4.5 安全边界

- MCP 进程以当前用户权限运行，**不是沙箱**；工具描述必须写明这一点。
- 只读工具默认可用；写工具需显式启用，并在文档和工具元数据中说明其影响与宿主审批前提。工具标注只是提示，不能作为审批执行证据。
- 不新增监听端口（stdio 传输），不引入跨机访问。
- 结构化观测沿用现有脱敏路径，不新增凭据、连接串或 env 值字段；日志来自应用输出，不能保证其中绝无敏感值，需限制返回量并在文档说明。

## 5. 交付物与验收

交付物：

- `internal/mcp`（新包）+ `stackharbor mcp --stdio` 子命令。
- `docs/mcp.md`：工具、参数、MCP `structuredContent` 与 `isError`、业务错误码、CLI 退出码对照、最小客户端配置及写工具审批前提示例。
- 测试：单元测试（参数校验、错误映射、无会话路径、版本降级）、集成测试（真实会话只读路径；写路径启用门槛、共享使用者阻断、冲突/过期/幂等与会话结束恢复）。
- 一次真实客户端冒烟，记录到忽略目录 `docs/verification/`。

验收标准（逐条可测）：

- [ ] 无会话时 `discover` / `validate` / `plan` / `sessions` 可用；`operation` 仅在已知 ID 有会话结束完成记录时返回结果，否则返回结构化未决或未找到错误。
- [ ] 无会话时 `status` / `logs` 返回结构化"无可用会话"错误；显式启用写工具后，`session_plan` / `apply` 也如此（含前置条件说明，不是纯文本或 panic）。
- [ ] 默认不暴露写工具；显式启用后，真实客户端按其审批策略展示计划并逐次审批 `apply`。没有经过验证的宿主审批时，文档不宣称强制人工确认。
- [ ] `session_plan` 返回共享资源影响和部分覆盖告警；`apply` 提交前重新检查，遇到其他工作区活动使用者时拒绝，且没有调用会话 `Submit`。
- [ ] `apply` 缺少 `plan_id` 或 `idempotency_key` 被拒；用错 key 报冲突。
- [ ] 同一 `plan_id` + 同一 `idempotency_key` 重放返回同一 operation。
- [ ] 过期计划返回 `plan_expired`，不会自动重建计划后执行。
- [ ] 日志 `gap` / `dropped` / `reset` / `next_cursor` 与 `Partial` 观测在结构化结果里显式可见。
- [ ] 工具执行错误使用 MCP 的 `isError` 和稳定业务错误码；CLI 退出码仅作为文档对照，不充当 MCP 调用结果。
- [ ] 不改变现有 CLI 行为、退出码与 JSON 输出；现有测试全部通过。
- [ ] `make check` 通过；若新增依赖，[THIRD_PARTY_NOTICES](../THIRD_PARTY_NOTICES) 与 `licenses/` 同步更新。

## 6. 里程碑与粗估工作量

| 里程碑 | 内容 | 粗估 |
| --- | --- | --- |
| M1 | 确定 MCP SDK/协议版本、只读工具输入输出与错误映射 | 0.5–1 人日 |
| M2 | 7 个只读工具 + `docs/mcp.md` 初稿 + 单元测试 | 1.5–2 人日 |
| M3 | 真实客户端只读试用；核对工具命名、结果体积和无会话体验，再决定写工具是否进入一期发布 | 0.5–1 人日 |
| M4（有条件） | 显式启用的两阶段写路径、共享资源检查复用、宿主审批验证和冲突/过期/幂等测试 | 需单独估算 |
| M5 | 契约定稿、文档评审和发布（CHANGELOG `Added`，README 双语各加一行使用说明） | 0.5–1 人日 |

只读试点粗估约 3–4 人日；写路径需在 M3 后按共享检查复用范围、SDK 和目标宿主审批能力重新估算。里程碑不含 DSH 插件。

## 7. 风险与开放问题

| 风险 / 问题 | 现状 | 处理方向 |
| --- | --- | --- |
| MCP Go SDK 依赖 | 目前 [go.mod](../go.mod) 没有任何 MCP/JSON-RPC 依赖 | M1 决定：引入官方 SDK，还是用最小 stdio JSON-RPC 实现；两者都要评估许可与依赖面 |
| 宿主审批不可由 MCP 服务端通用验证 | `plan_id` 与幂等键可由 agent 自行取得，工具标注也只是提示 | 写工具默认关闭；逐次审批交给经真实客户端验证的宿主，文档按实际保证能力表述 |
| 直连会话协议遗漏跨工作区检查 | 现有 `inventory.SharedImpact` 在 CLI/Web 网关层执行 | 计划呈现影响，提交前复查并阻断活动共享使用者；增加跨工作区集成测试 |
| 长时操作与客户端超时 | CLI 默认 `--timeout 10m`，超时不代表操作失败 | 工具返回 operation 句柄与查询指引，不阻塞等待终态 |
| 多 agent 并发操作同一会话 | 计划 60s TTL + fingerprint 变化即冲突 | 文档写清楚"冲突是正常的，重新计划"；不引入自动重试 |
| 观测术语被误读 | `Listening` / `Running ext` / `external` 语义已有严格定义 | 工具描述逐字沿用现有口径，不复述成"服务已就绪" |
| 无会话的体验 | 用户必须先自己开一个终端 | 一期只如实报错并给出 `stackharbor --root <path>` 的提示；自动拉起会话属于二期前置 |
| 文档语言 | 仓库内 plan/proposal 类文档为中文，`web-control.md`/`development.md` 为英文 | 本期沿用中文；如需上游发布英文版，作为 M5 的独立任务 |

## 8. 二期（暂不实施）：DSH 插件

### 8.1 为什么暂缓

- MCP 先把跨客户端只读接口做出来；写路径达到第 4.3 节门槛后再复用。没有接口就做 DSH 插件，容易在 CLI 上再包一层文本解析。
- 社区同类插件已覆盖大部分表层功能（第 8.3 节），先做会撞车且边际价值低。
- DSH 插件无法自己拉起服务：服务存在于需要 TTY 的会话里（`internal/cli/run.go:150`），所以插件必须先解决 PTY 托管会话，这本身就是一块独立基建。

### 8.2 触发条件（全部满足才启动）

- 一期 MCP 已在真实工作中使用，且接口未出现需要重构的问题。
- 出现明确、重复的用户诉求（例如"不想离开 DSH 就能看端口和日志"），而不是推测的需求。
- PTY 托管会话能力已单独评估并接受其安全面。

### 8.3 形态与依赖关系（记录备查）

- 形态：DSH bundle = 打包现有 skill + 预置 MCP 配置 + 可选只读 UI 面板；**业务逻辑复用一期 MCP 工具**，不另写一套。
- 允许：只读面板、工具透传。
- 不允许：绕过一期另写业务逻辑、绕过会话计划和共享资源检查；需要人工确认的写操作必须由已验证的宿主审批策略覆盖。
- 分发：npm bundle + GitHub `dsh-plugin` topic；安装为 `dsh plugin --profile <name> add <pkg>`。

### 8.4 生态现状（2026-10-05 快照，供决策用）

- 社区注册表 `https://awesome-dsh-plugin.com/plugins.json`：4414 个插件，22 个分类，其中 `dev` 分类 318 个。
- 与本项目功能最接近的：`dsh-farm`（`farm.yaml` 声明式服务 + 启停日志工具 + 侧栏抽屉）、`dsh-service-console`、`dsh-port-manager`、`dsh-compose-panel`、`dsh-docker`、`dsh-side-monitor`、`dsh-terminal`。
- 注册表中**没有**任何插件提及 StackHarbor，位子仍空着。
- `dsh-farm` 的差距（即 StackHarbor 的差异点）：无端口归属观测、无依赖图与条件、无 plan/确认、无会话所有权与外部端口 release、无 Compose 多文件资源。

## 附录 A：本文事实来源

源码：

- [internal/cli/run.go](../internal/cli/run.go)（usage、交互模式 TTY 检查、命令分发）
- [internal/cli/control.go](../internal/cli/control.go)（控制命令、`--yes`/`--dry-run`、`controlFailure` 退出码映射）
- [internal/cli/output.go](../internal/cli/output.go)（discover/validate/plan/snapshot/operation 的 JSON 形状）
- [internal/control/types.go](../internal/control/types.go)（`Snapshot`/`Node`/`Port`/`Plan`/`Operation` 字段）
- [internal/control/plans.go](../internal/control/plans.go)、[operations.go](../internal/control/operations.go)（TTL 60s、计划上限、幂等键、保留策略）
- [internal/inventory/impact.go](../internal/inventory/impact.go)、[internal/cli/control.go](../internal/cli/control.go)、[internal/web/proxy.go](../internal/web/proxy.go)（跨工作区共享资源影响与提交前检查）
- [internal/control/logs.go](../internal/control/logs.go)、[internal/sessionhost/completion.go](../internal/sessionhost/completion.go)（日志缺口字段与会话结束后的完成结果恢复）
- [internal/sessionapi/server.go](../internal/sessionapi/server.go)、[client.go](../internal/sessionapi/client.go)（`/v1/*` 路由与客户端方法）
- [internal/graph/plan.go](../internal/graph/plan.go)（离线计划 `schema_version: 2`）

文档：

- [Web 与 CLI 控制](web-control.md)、[协议 v2](protocol-v2.md)、[详细使用说明](usage.zh-CN.md)、[开发与文档约定](development.md)

实测命令（2026-10-07，`dist/stackharbor` 0.6.0）：

```sh
stackharbor --version
stackharbor discover --root examples/harbor-cafe --json
stackharbor validate --root . --json
stackharbor plan start --root examples/harbor-cafe --target menu/service/api --json
stackharbor status --root examples/harbor-cafe --json ; echo $?   # 1，人类可读输出
stackharbor --root examples/harbor-cafe </dev/null ; echo $?      # 2，Interactive mode requires a terminal
# 另用一个含无效 stackharbor.yaml 的临时目录验证：discover --json 仍输出 diagnostics，退出码 2
```

DSH 侧结论依据（本期不使用，仅记录）：

- DSH 源码检出 `/Users/kimi/opensource/deepseek-harness`：`docs/user/develop/basic/{index,publish,tool}.md`、`docs/subsystems/{mcp,skills,terminal,subprocess}.md`、`packages/shell/shell/README.md`。
- 社区注册表快照与本地已安装插件清单（`~/.dsh/profiles/desktop`）。
