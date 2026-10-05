# 协议 v2 实施计划

设计日期：2026-10-04；实施更新：2026-10-05。核心协议与编排已实现，真实数据库协调已获授权并完成，PageSmith 生命周期验收通过、默认注册切换为 v2。当前支持范围见 [v2 参考](protocol-v2.md)，验收状态见 [验证记录](verification/protocol-v2.md)。

关联规格：[协议与编排 v2 草案](protocol-v2-proposal.md)。以下为原验收目标；已验证、受限能力及待完成项以验证记录为准。

## 交付原则

模型→验证→纯计划→执行→观测→导航的顺序推进。调度与界面不能各建一套依赖关系。每一阶段以临时工程完成验收，达到门槛后才接入真实项目；不通过绕开迁移或直接 stamp 让启动“看起来成功”。

第一阶段使启动顺序和失败来源可表达；第二阶段使写任务可受控执行；第三阶段把真实子工程从混合脚本接入该契约。v1 支持始终保留。

## A. 节点与协议

涉及：`internal/model`、`internal/config`、`internal/discovery`、`internal/cli/output.go`。

交付：

- 用明确的 NodeID、NodeKind、DependencyCondition 和 Context 表示 resource/task/service，不通过空 ports 或 command 名猜类别。
- project 独立包含 tasks/services，workspace 包含共享 resources；编译为一份 node 集合，保留 source file 和字段位置。
- v2 严格解码；service.ready、task.check/run/verify、resource.available 是不同契约，不共享含混的状态字段。
- v1 经单独兼容转换器进入内部图，保留原 ID 的 CLI 兼容 alias，双向 alias 冲突拒绝。
- read-only 阶段拒绝 schema-write/when-needed 等未实现能力；能力校验与版本解析分离。
- 注册上下文一次合成，所有阶段使用同一快照；JSON 不输出 env/连接串凭据。

先复现：任务无法表达、一次性成功退出被当成失败、带依赖条件的配置被拒绝。验收覆盖条件/类别不匹配、unknown 字段、重复 ID、根外 cwd/env_files、缺失环境文件、格式错误和能力尚未实现。

## B. 统一 DAG 与只读计划

涉及：`internal/graph`，新增 planner；Compose 适配器在 discovery 与 planner 之间提供已规范化节点。

交付：

- 一份 DAG 计算 start 的传递依赖闭包、stop 的运行后继闭包，以及 restart 的原运行集合。
- 导出稳定拓扑层级及显式边，不只导出服务 ID 数组；项目展示投影不替代原图。
- operation plan 冻结 config_digest、目标、动作、层级、影响与待核验项；命令绑定稳定 NodeID。
- 相同请求可复用同一节点 attempt；同一节点的同时 stop/start/restart 不允许产生竞争实例。
- `plan` 只读取配置/已有观测，不执行配置中的命令。Compose 配置解析如需调用解析命令，必须关闭隐式 build/pull/up，并保证不输出插值后的秘密。

先复现：Admin 排在上游前、shared migration 在多个目标里重复、跨项目合法 DAG 被错误项目树扭曲。验收覆盖菱形共享依赖、同级稳定排序、缺失节点、环、多文件 Compose 依赖条件、外部资源以及 dry plan 无副作用。

## C. Task runner 与状态传播

涉及：`internal/runner`、`internal/supervisor`、`internal/model/runtime.go`；新增 task executor，复用已有受管进程身份及日志排空能力。

交付：

- 服务保持长期存活监视；task 等待退出、排空日志并处理残留后代，成功完成才发 succeeded。
- 节点进入等待不占并发槽；依赖/锁/执行/就绪分别有 timeout 和计时信息。
- blocked 是未执行的下游结果，不伪造进程退出码；记录直接 blocker 及完整路径。
- 一条分支失败不停止独立分支；计划返回整体成功/部分成功/失败/取消及每节点结果。
- 上游运行后失去就绪区分 degraded；恢复/重试重新检验条件，没有默认无限重试。
- operation cancellation 与会话退出分别处理：取消当前计划不停止此前已运行的无关服务。
- generation 与 attempt ID 防止旧 watch/check 结果覆盖新计划。

先复现：任务退出 0 被判失败、下游未 spawn 却 failed、等待依赖可能无界。用临时 fixture 验证退出 0/非 0、超时、取消、残留子进程、部分分支失败、上游反复失去/恢复就绪以及资源统计不把 task 历史 PID 当活进程。

## D. 迁移检查与资源锁

涉及：task executor，新增 lock/target adapter；不把 Alembic 专用识别写进通用 supervisor。

交付：

- when-needed 的完整 check→run→verify 契约与结构化诊断解析；未知退出码/无效 JSON 不得放行。
- 解析冻结上下文中的真实数据库身份；排他锁按目标，取锁后重查；输出不含秘密。
- 本机跨 workspace 锁；PostgreSQL advisory lock 支持须保持持锁连接贯穿阶段，连接丢失取消执行并把结果记为 unknown。
- 同一计划的共享 task 去重；不同计划等待已有 attempt 后重新 check，而非盲信历史成功。
- drift 不自动 stamp/drop；检查范围、版本集合、目标变动和状态失效明确。
- 运行消费者存在时先维护计划；先停已知受管消费者，外部影响无法处理时阻塞，不默认在线 schema 修改。

先复现本次问题的安全副本：临时库先创建 content_sources，但登记旧 revision，check 应返回 drift 且 migration run 的调用次数为 0。不能用真实库作为 RED fixture。

验收还包括：新库、正常落后、已到目标、多个 heads、verify 失败、连接串同目标不同凭据、alias、多 workspace 竞争、锁失效、迁移中断和不同目标并行。

该阶段的隔离 PostgreSQL 测试环境在启用前明确用途和清理范围，不使用 PageSmith 的既有数据卷。没有环境时报告未验证，不能用 mock 锁代替真实并发验收。

## E. 可恢复记录与控制边界

涉及：supervisor actions、Docker adapter、logs；新增 operation record store。

交付：

- 0600 的有界记录文件与轮转；operation/attempt/阶段/阻塞链和日志关联，重启工具可阅读历史。
- 脱敏既要覆盖连接串，也要覆盖 task JSON 和原始 stderr；未识别的应用秘密不能宣称完全受保护。
- resource 的 managed/observe、persistent/session 和 observed identity 分开；不同子工程共享资源不因单工程停止被停。
- 全部启动/停止/重启与单工程动作共用 planner；作用范围在确认界面具体列出。
- restart 只恢复原运行的受影响集合，不强制重启基础设施或 seed；新库状态始终先 check。
- q 清理会话进程，容器控制是显式动作；不把 migration succeeded 的任务当作可以逆序回滚。

验收覆盖取消前后边界、PID/容器身份改变、外部资源、共享资源停机、旧记录恢复和截断/损坏记录；保留现有 port takeover 与进程清理回归。

## F. Sidebar 与故障工作区

涉及：`internal/tui/model.go`、`view.go`、`keys.go`、`dashboard.go`、`project.go` 和 Docker panel。

交付：

- 总览固定首项，resource→task→service 依赖层级来自 planner；项目可折叠，单节点以 NodeID 保持焦点。
- 多上游显示边与归属，cross-project 合法 DAG 按阶段分组，不假造一棵工程父子树。
- 任务页显示 checking/running/verifying、退出结果和结构化诊断；工程页保留日志，增加计划进度及阻塞链。
- “失败”与“未启动，受阻塞”不同文案和符号；成功检查但没有执行迁移显示“已满足，未执行”。
- 单行快捷栏随当前节点类别变化，任务没有无意义的打开端口/服务重启行为；重试与查看前置项入口清楚。
- 保留页面统一内边距、蓝色粗体按键、固定侧栏背景边界、NO_COLOR 和窄屏切换。

验收覆盖 120/80/60 列、中文/长名称、帮助/确认页、折叠展开、动态状态、依赖排序后按键操作对象不变，以及合法项目投影环。

## G. PageSmith 接入与数据库协调

协议接入与真实数据协调是两项独立交付。

1. 阅读目标工程的最新 AGENTS 和运行契约，核对当前分支/dirty work、真实 Compose project、env 加载顺序、所有建表入口。
2. 为 migration_status 设计明确的读取范围与返回契约，禁止调用触发 create_all 的 store 初始化；read-only 数据库连接约束其检查。
3. 在隔离副本覆盖版本正常落后和结构漂移；有证据后新增 task 注册，保留旧 run.sh 的独立兼容入口。
4. API/worker/relay/beat 注册为各自的前台服务，避免 task 与原 local runtime 脚本重复迁移；就绪探针必须针对该实例，不拿其他 worker 的响应算自己的成功。
5. 核对真实数据库已有表、索引、外键、约束、数据迁移效果和版本链；先备份，再形成具体的数据协调计划。仅表存在不构成已执行所有后续数据迁移的证据。
6. 数据协调及真实写迁移另行获得授权后执行。既不能简单跳过全部迁移，也不能直接 stamp 当前 head。
7. 验证真实启动→迁移检查→应用就绪→前端启动，再验证普通重启、单工程停止和 q 清理，记录容器及原外部服务的状态。

通过前，不更新 easy_study_pipeline 的默认启动注册，也不把未验证的 v2 功能安装成当前用户默认命令。

## 验收与文档交付

每阶段先用最小 fixture 建立失败证据，再验证对应行为。阶段内运行相关测试，集成后运行 repository race tests/vet、跨平台构建和实际 PTY 检查。Linux 实机、PostgreSQL 锁与业务项目各自记录验证状态，不用交叉编译或 mock 代替实测。

最终同步协议参考、注册 skill、CLI 帮助、examples、README、CHANGELOG 和验证记录。版本号按实际发布授权更新；本方案不授权 commit、push、release 或数据变更。
