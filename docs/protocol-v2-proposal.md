> 2026-10-05 实施更新：核心实现、获授权的数据库协调、真实运行验收及默认注册切换已完成；[当前支持范围](protocol-v2.md)与[验证记录](verification/protocol-v2.md)列出实际能力边界。本文保留原设计决策。

# StackHarbor 协议与编排 v2 草案

设计日期：2026-10-04。状态：原始设计记录；已实现字段和命令以当前 v2 参考为准，未支持项不能据本草案直接使用。

## 目标

用户选择一个工程启动时，应能看到为什么要启动它的上游、当前在检查什么、迁移是否需要执行、失败发生在哪一步，以及哪些下游尚未启动。导航、启动、停止、重启和日志使用同一份依赖图。

各子工程声明自己的生命周期与迁移契约，StackHarbor 负责计划和执行。工具不能根据 Python、Node 或目录名称猜迁移命令，也不能把整个仓库的迁移强行归并到一个全局命令。

## 当前实现的真实缺口

| 当前行为 | 后果 | v2 改进 |
| --- | --- | --- |
| 已有服务拓扑排序及依赖就绪等待 | 并非完全没有先后顺序，但只理解长期服务 | 增加一次性任务和基础设施节点，明确等待条件 |
| backend 注册 `./run.sh local` | Docker、迁移、API、worker、relay、beat 合在一个根进程里 | 拆分基础设施、迁移任务和各个常驻服务 |
| 一次性命令没有独立模型 | 迁移退出 0 也会被服务监视器当作根进程退出 | 任务用成功结束释放下游，服务用就绪释放下游 |
| 依赖失败把下游记作 failed | 看起来像多个工程分别启动报错 | failed 与 blocked 分开，记录阻塞链及是否真正执行过命令 |
| 工程按 ID 字母排序 | Admin 排在它依赖的 backend 前面 | 导航按同一图的依赖层级显示 |
| Docker 依赖有单独的排序与控制路径 | 无法完整显示容器→迁移→应用的计划 | 适配器节点归入统一执行图 |
| 只报告根进程退出码，日志仅在内存 | 用户只能在长 traceback 尾部猜原因，退出后丢失现场 | 阶段、结构化原因和操作记录独立于原始日志 |
| 没有迁移状态检查和资源排他锁 | 重复启动可能重复迁移，不同工程可能同时改同一库 | 在锁内检查、执行、复验；同一目标串行 |

本次真实案例：PostgreSQL 的 `alembic_version` 是 `e42b1c7d9a10`，但 `content_sources` 和后续表已存在；启动迁移尝试执行下一条建表脚本 `f53c1a8d7b20`，报 `relation "content_sources" already exists`。db、redis 健康并不代表数据库迁移状态正确。

已有启动路径同时包含迁移和 ORM 建表能力。主应用 lifespan 已将 ORM 建表改为显式开关，但其他入口仍有建表调用；不能凭当前代码断言历史上是哪一个入口产生了这些表。数据库现状的协调属于子工程修复，需要独立核对，协议升级本身不会使旧库自动恢复。

## 三类执行节点

| 类别 | 例子 | 成功条件 | 停止语义 |
| --- | --- | --- | --- |
| resource | Compose db、redis；外部数据库 | 适配器确认可用及身份；健康未知不能伪装为健康 | 区分可管理与仅观察、会话资源与持久资源 |
| task | 环境检查、数据库迁移、必要的构建、显式初始化 | 有界命令退出成功，必要时复验成立 | 取消在执行的任务，不能把已完成迁移当作可回滚服务 |
| service | API、worker、relay、beat、前端 | 根进程存活且声明的就绪检查通过 | 按依赖逆序清理有身份记录的受管进程 |

统一节点引用：`resource/<id>`、`<project>/task/<id>`、`<project>/service/<id>`。任务与服务可以同名而不冲突。项目是导航和注册归属，不是执行节点，也不默认等同于一套数据库。

依赖明确声明条件：

- `available` 只用于资源；结合资源自己的健康契约。
- `succeeded` 只用于一次性任务；迁移命令退出 0 后还必须通过它声明的复验。
- `ready` 用于有就绪检查的服务。
- `started` 用于明确只需要根进程存活的服务；不可悄悄降级 `ready`。

缺失节点、条件与类别不匹配、依赖环、同一服务重复端口、含糊的资源身份在执行前报错。等待中的节点不占启动并发槽，防止上游因所有槽被下游占满而无法启动。

## 注册示例

以下为目标协议示例。`scripts/stackharbor/*.py` 是需要在子工程新增的适配脚本，并不是已经存在的脚本。用户已有 `run.sh` 可以保留为独立启动入口，StackHarbor 接入避免在 task 和 service 中重复执行同一迁移。

workspace 中只放共享资源和导入关系，不放所有工程的迁移命令：

```yaml
version: 2
registrations:
  - .stackharbor/backend.yaml
  - .stackharbor/admin.yaml
  - .stackharbor/studio.yaml
  - .stackharbor/study.yaml
resources:
  database:
    name: PostgreSQL
    adapter: compose
    file: compose.yaml
    project: pdforge
    service: db
    available: healthy
    lifetime: persistent
    control: managed
  redis:
    name: Redis
    adapter: compose
    file: compose.yaml
    project: pdforge
    service: redis
    available: healthy
    lifetime: persistent
    control: managed
```

Compose file 与 project 必须来自实际项目配置，不从目录显示名猜测；接入时核对真实 Compose project，而不是直接复制示例值。多个子工程引用同一资源节点，不各自重复注册同一个容器。外部资源使用 `control: observe`，没有自动启动或停止动作。

backend 注册自己的检查、迁移与服务：

```yaml
version: 2
project: {id: backend, name: PageSmith backend}
context:
  cwd: ../backend
  env_files: [../.env, ../backend/.env]
tasks:
  preflight:
    name: 检查运行环境
    run: {command: [uv, run, --no-sync, --offline, python, -B, scripts/stackharbor/preflight.py]}
    timeout_seconds: 30
    effect: read-only
    policy: always
  migrate:
    name: 数据库迁移
    requires:
      - {node: backend/task/preflight, condition: succeeded}
      - {node: resource/database, condition: available}
    effect: schema-write
    policy: when-needed
    lock:
      scope: database
      target: {env: PAGESMITH_DATABASE_URL}
      schema: public
    check:
      command: [uv, run, --no-sync, --offline, python, -B, scripts/stackharbor/migration_status.py]
      satisfied_exit: 0
      needed_exit: 10
      drift_exit: 20
    run: {command: [uv, run, --no-sync, --offline, python, -m, alembic, upgrade, head]}
    verify:
      command: [uv, run, --no-sync, --offline, python, -B, scripts/stackharbor/migration_status.py]
      success_exit: 0
    timeout_seconds: 180
services:
  api:
    run: {command: [uv, run, --no-sync, --offline, python, server.py, --no-open, --reload]}
    requires:
      - {node: backend/task/migrate, condition: succeeded}
      - {node: resource/redis, condition: available}
    ports: [{name: http, port: 5600}]
    ready: {tcp: "127.0.0.1:5600", timeout_seconds: 60}
    stop: {signal: INT, timeout_seconds: 15}
  worker:
    run:
      command: [uv, run, --no-sync, --offline, python, -m, celery, -A,
                "app.infrastructure.async_tasks.celery_app:celery_app",
                worker, --loglevel=INFO, "--queues=system,fetch,process,llm,export",
                "--concurrency=2"]
    requires:
      - {node: backend/task/migrate, condition: succeeded}
      - {node: resource/redis, condition: available}
    # 未实现按实例校验的 worker readiness 前，只能标为“存活，未检测就绪”。
```

relay 和 beat 同样各有前台命令、资源依赖和停止策略，不隐藏在 API 根进程中。只有真实有需要的服务才依赖 API；worker、beat 等不应为了视觉排列而被强制串联。

前端继续由其自己的工程注册：

```yaml
version: 2
project: {id: admin, name: Admin console}
context: {cwd: ../frontend/admin_console}
services:
  web:
    run: {command: [pnpm, dev]}
    requires:
      - {node: backend/service/api, condition: ready}
    ports: [{name: http, port: 5611}]
    ready: {tcp: "127.0.0.1:5611"}
    open: "http://127.0.0.1:5611/"
```

task 可覆盖 context 的 cwd/env，所有相对路径按注册文件解析并受 workspace 根限制。环境合成顺序固定为进程环境→按顺序的 env_files→context.env→节点 env；显式文件不存在、语法错误或目标身份无法解析均报错。字段值不做隐式 shell 插值，命令仍是 argv。check、run、verify 使用同一个已冻结的上下文，避免检查一个库、修改另一个库。

本例要求数据库连接配置在此上下文中明确提供。不能仅由 run 脚本内部设置默认连接串，让锁与 check 看不到实际目标。`effect: read-only` 是接入契约，不是执行任意命令的安全沙箱；检查脚本不得触发 ORM 建表、自动迁移、安装依赖或业务 seed。示例检查禁用 uv 自动同步和 Python 字节码写入；所需环境缺失时报告缺失，由独立的环境准备 task 处理。

环境准备、构建和 seed 不都属于数据库迁移。首次安装依赖是 `effect: environment-write`，默认 manual；必要构建可用 when-needed，check 比较锁文件/输入与真实产物；seed 必须有业务定义的幂等键和 verify。不能因为本会话执行过一次就跨换分支永久跳过。

Compose 适配器读取实际生效的 file/project/profiles 和依赖条件，不能只摘出服务名后丢失条件。健康依赖需要 healthcheck；容器启动与容器健康分别表示。Compose 的一次性初始化服务按 task 的成功结束处理，不混入常驻资源。不支持的扩展依赖字段必须报出限制，不能静默变成“运行即健康”。

## 迁移契约与并发

迁移是子工程拥有的任务，不是工具内置的“启动前统一跑一次”。StackHarbor 不解析任意迁移框架的业务版本语义，接入脚本负责给出可靠状态：

| check 结果 | 调度动作 |
| --- | --- |
| 0：当前结构与目标迁移状态满足契约 | 本次检查成功，记录依据；不执行 run |
| 10：版本链可升级，前置结构一致 | 执行 run，然后 verify；仅复验成功才释放下游 |
| 20：实际结构与版本记录冲突 | task failed/drift，下游 blocked，保留诊断，停止自动推进 |
| 其他退出码、超时、无法访问数据库、格式不合法 | 检查失败，不能按“需要迁移”处理 |

状态脚本不得仅凭 `current == head` 宣称结构正确；应声明它验证的范围、发现的结构冲突和不能验证的部分。至少必须能识别本次“下一条迁移需要新建的表已经存在”的情况。不同框架各自实现检查契约，不能将普通 `alembic current` 的退出 0 当作“已经到 head”。

结构化输出是单个有大小上限的 JSON 对象，stdout 仅用于该对象，人工日志写 stderr；输出必须与退出码一致。示例：

```json
{
  "schema_version": 1,
  "status": "drift",
  "code": "migration.schema_conflict",
  "message": "content_sources 已存在，但对应迁移尚未登记",
  "current_revisions": ["e42b1c7d9a10"],
  "target_revisions": ["目标版本由适配器实际解析"],
  "checked_scope": ["migration_chain", "pending_create_table_conflicts"],
  "remediation": "核对现有结构与迁移记录后协调，不自动删除表或 stamp"
}
```

多 head 使用版本集合，不能只比一个字符串。框架不支持完整结构核对时，明确 checked_scope，并以配置的最低检查要求决定能否放行；不能将“未检查”编码成“结构一致”。

schema-write 强制要求 check、verify、有界 timeout 和可解析的 target 锁。`policy: always` 不允许用于 schema-write。数据 seed、索引重建等具有写副作用的任务也需明确的检查/复验与重入契约，或只能 `policy: manual`，不自动挂到常规启动上。

锁流程：解析目标身份→等待资源可用→获取锁→再次 check→按结果执行→verify→记录结果→释放锁。不能先在锁外检查，再把旧结果带入执行。相同数据库的不同任务默认串行；依赖关系决定业务顺序，锁本身不决定先后。

锁不只按 task ID 或工作目录划分。目标身份包含 driver、规范化 host、port、database、schema；`scope: database` 的排他键只取数据库身份，不含 schema，避免同库不同 schema 的任务并行改公共对象。用户名/密码不能用于区分同一目标，凭据不写入锁文件、日志或 JSON。可选 `lock.shared_id` 用于 host 别名或多连接串指向同一库的场景，同一 ID 对应不一致目标时拒绝执行。

本机跨 workspace 使用操作系统排他锁，退出释放。跨主机不能靠本机文件锁保证；PostgreSQL 适配器必须使用目标数据库级 advisory lock，并在整个检查/执行/复验期间保持持锁连接。尚无数据库级适配器时，必须明确只提供本机互斥，不能宣称支持跨主机并发迁移。

成功结果不是永久的 boolean。每次新的启动/恢复计划都重新 check；配置、目标迁移版本、数据库身份/容器代次变化会使历史结果失效。一次计划内共享迁移 task 只检查/执行一次，所有相关下游等待同一个 attempt。

需要迁移且当前已有消费者运行时，先生成维护计划，展示会暂停和恢复的 API/worker/前端范围，处理在途工作；第一版不默认支持在线 schema 变更。用户确认维护范围后先停止已知消费者，再在锁内重查并执行。外部消费者不能自动停止，进入需要人工处理的阻塞状态。目标锁只排斥同样遵守锁协议的迁移，不能保证阻止任意应用写数据；适配器和维护契约必须说明检测范围，不能宣称本机进程图覆盖数据库的所有客户端。

不自动删除已有表、修改 Alembic version 或 stamp head。恢复数据卷、换分支、旧进程创建过表等场景都先按 drift 停下，进入子工程的数据协调流程。

## 计划、执行与恢复

1. 解析所有注册和资源，构建统一 DAG；默认 UI 打开只观察。
2. 用户启动某服务或工程，计算目标服务和传递依赖；工程默认启动自己的所有 service，task 仅作为依赖执行，manual task 不包含在默认目标中。
3. 生成不可变操作计划，含 operation ID、配置摘要、目标、依赖闭包、潜在写任务、冲突端口、受影响运行节点；执行前解决端口接管等需要确认的动作。
4. 应用计划前复核配置与运行身份。独立分支可并行，资源可用→task 成功→service 就绪这条边严格串行。
5. task 失败只阻塞其后继；无关分支继续。计划结果为成功/部分成功/失败/取消，不能因为最先成功的节点返回就宣称整体成功。
6. 重试从失败节点的依赖闭包恢复；已健康且不受影响的服务保留。task 重新 check，禁止未经检查重跑写操作。

v2 新增只读 `stackharbor plan start|stop|restart --target <node-id> [--json]`。只读计划不执行 check 命令、启动 Docker 或下载依赖；只能标出“迁移状态待运行时核验”，不能预先宣称可以跳过。`validate` 检查结构，`doctor` 显式执行 read-only 检查契约并报告能力范围，`task run <id>` 执行显式任务。上述命令为拟议接口，第一阶段没有实现的命令不得提前公布为可用。

计划包含 config_digest、targets、ordered_layers、edges、actions、affected_running、requires_confirmation、unresolved_observations。dry plan 的观测结论不充当后续执行的永久授权；执行时重新核验身份及配置。schema-write 的决策分支只在锁内 check 后确定。

等待依赖、等待锁、task 执行和 service 就绪有独立有限预算，UI 展示当前阶段与耗时；默认依赖等待 600 秒、锁等待 60 秒、task 120 秒、就绪 60 秒，注册可覆盖但不得无界。等待超时下游变为 blocked/wait_timeout，用户可在上游恢复后发起新的恢复计划，不能依赖永不结束的 Start 调用。

取消、重启与停止：

- 取消后禁止调度新节点；只清理该计划仍在执行的命令和明确创建的会话资源，保留既有健康服务。
- schema-write 被中断/超时后结果记为 unknown；检查数据库当前状态后再决定能否继续，不假设信号退出等于事务回滚。
- task 根进程退出成功但还残留受管后代时，不算完成；先完成清理。任务 timeout 覆盖 check/run/verify 的整个 attempt，等待依赖与锁另外显示预算，不允许无限等待。
- 重启 API 默认只重启 API 和原来运行的受影响下游；不重启 db/redis、不重建容器、不强制重跑成功迁移。新的启动计划仍重新 check。
- 停止服务按依赖逆序，不对成功 task 运行“逆命令”。普通退出只清理会话进程；持久资源保持。
- 一键全部停止/重启的计划明确展示是否包含容器、哪些容器是共享/既有、哪些下游受影响。共享持久资源不能因一个子工程停止而被顺手停止；单独容器控制保留。
- 外部服务可观察；明确声明 observe 依赖并通过其健康/身份契约时才满足边。观测内存或发现端口不等于有权停止，也不等于它的 schema 兼容。
- 上游运行后失去就绪，下游标为 degraded 并展示原因，默认保留现场；新的下游不放行。显式停止上游仍需先处理受影响的受管下游，不自动杀外部进程。
- 自动重启与写任务自动重试不纳入第一阶段，不能用无限重试掩盖 drift。

service 默认 `control: managed`，需要前台 run.command；`control: observe` 禁止 run/stop 副作用字段，必须提供可核验身份和健康契约，计划不为它生成启动、停止、重启动作。依赖引用同样的 service 节点，不用另设一套只由端口决定的外部服务判定。

## 可观察状态

service：stopped→waiting→starting→ready；异常为 failed/unready/degraded，停止为 stopping。没有 probe 时使用 started，不能对外报告 ready。

task：pending→waiting→checking→running→verifying→succeeded；检查满足时 checking→succeeded 并注明“已满足，未执行”。异常为 failed/canceled/unknown。任务停止不清除已执行过的业务结果。

resource：unknown/unavailable/starting/available/unhealthy，另有 control 和 lifetime，不与 service root PID 混为一谈。

blocked 是调度结果：节点尚未执行，记录直接 blocker 与完整链。例：`Admin → backend/api → backend/migrate → schema drift`。被阻塞的前端显示“未启动：等待后端迁移修复”，不显示自己的命令失败或无意义的退出码。

attempt 记录 operation ID、node ID、阶段、开始/结束时间、退出码、是否 spawn、是否实际执行写任务、阻塞链、观察来源、经过脱敏的诊断和日志范围。诊断使用 code/message/remediation，未知错误保留真实退出码与原始日志，不随意从最后一行 traceback猜根因。

操作摘要和任务日志支持有界落盘与再次读取；凭据脱敏、0600 文件权限、轮转与保留策略需一并实现。不能只记录最后 20 行，从而把最早的错误截断在屏幕之外。

## Sidebar 与右侧信息

Sidebar 使用统一 DAG，不另设与调度无关的显示顺序。总览固定最上；基础设施先于依赖它的任务，迁移先于应用，应用先于依赖它的前端。相同层级采用稳定名称/ID 排序，状态更新不随意移动焦点。

```text
总览

基础设施
  PostgreSQL         健康
  Redis              健康

PageSmith backend
  环境检查           已通过
  数据库迁移         失败：结构冲突
  API                阻塞：迁移未通过
  Worker             阻塞：迁移未通过
  Relay / Beat       阻塞：迁移未通过

依赖后端的工程
  Admin console      阻塞：API 未就绪
  PDForge Studio     外部运行 · 只读
  Study app          阻塞：API 未就绪
```

工程默认折叠显示摘要，可展开服务与任务。资源只出现一处，多上游用“依赖 PostgreSQL、Redis”或边列表展示，不用假树复制同一个节点。无关分支平级；仅作导航的候选项目排在“待注册”区。

服务级 DAG 无环不保证把所有服务折叠成工程后仍无环。跨工程交叉依赖时按节点层级显示工程的相应阶段，允许工程在不同层出现节点组，并保留工程归属；不能为了生成工程树而拒绝合法 DAG、隐瞒边或制造虚假的父子关系。

选中工程仍显示其日志工作区；选中任务显示 check/run/verify 和对应日志；总览显示本次执行计划与阻塞链，容器页仍可控制资源。使用统一内边距、背景边界和单行快捷栏；基于 node ID 保持选中项，避免排序后启动错对象。

## 兼容与实施顺序

v1 保持现有命令与 ID 的原语义，加载为 service 节点；现有 docker_depends_on 归一为 Compose 资源依赖。不能在升级时把 v1 run.sh 的副作用猜拆成 task。v2 使用新版本严格解码；未实现字段仍拒绝，不静默接受。

旧服务未配置 probe 时默认只有 started，v1 依赖行为保持兼容并展示检测缺口。v2 要求明确条件。discover/validate 输出增加节点、边与校验信息并使用 JSON schema_version 2；配置文件版本和 JSON 版本分别管理，不给旧消费方悄悄换形状。

第一阶段：节点/边模型、严格 v2 解码、只读计划、task runner 与完成门、blocked 状态、统一拓扑导航；用临时进程和临时数据库/资源验证。

此阶段只启用已实现的 read-only task；schema-write、when-needed 等未具备完整锁与复验能力时在校验阶段拒绝，不能先接受新字段后以简化 runner 执行迁移。

第二阶段：带检查/复验和目标锁的迁移任务、可恢复操作记录、资源持久性/管理边界、重启及取消的一致计划；未知与 drift 不放行。

第三阶段：接入 easy_study_pipeline，新增只读状态适配脚本，拆开 local runtime 的迁移与常驻进程。先核对并备份实际库、协调迁移漂移，再执行获授权的迁移和真实启动验收。协议接入与现有数据库修复分别报告。

## 必须通过的场景

| 场景 | 预期 |
| --- | --- |
| 新库启动一套后端和三个前端 | 基础设施健康后迁移一次；API ready 后释放前端；worker 等独立就绪 |
| 已到目标版本再次启动/普通重启 | 锁内 check；不执行迁移 run，不重启数据库 |
| 当前版本正常落后 | run→verify→成功；只退出 0 而 verify 失败仍不能启动应用 |
| content_sources 已存在但版本在建表之前 | drift；API 和前端 blocked；保留现有数据 |
| 两个子工程依赖同一迁移 task | 一个 attempt，多下游等待，不重复执行 |
| 不同 task/不同 workspace 指向同一数据库 | 目标锁互斥，取锁后重查；不同数据库可并行 |
| check 无权访问/返回未知码/超时 | 检查失败，不执行写命令，不算“待迁移” |
| 迁移期间取消、退出、断连 | 清理受管执行；结果 unknown；重试先检查现场 |
| db健康但 API未就绪 | 前端等待；阶段计时和原因可见，不能无限无说明等待 |
| backend迁移失败，另一个独立工程健康 | 独立工程保留；总览部分失败，不全停 |
| 外部 API/Compose 容器存在 | 观察、身份、兼容和控制权限分开；不因端口存在跳过任务 |
| 重启 API，之前只有 Admin 在运行 | 只恢复 API 与 Admin，不擅自启动其余前端 |
| 停止共享数据库 | 提示完整影响集合，逆依赖停受管应用；外部应用需明确处理 |
| 合法 DAG 投影成工程级环 | 节点阶段导航仍正确，不假造线性项目顺序 |
| 配置变更、换分支、数据卷恢复 | 历史成功失效，重新核验当前目标和结构 |
| 迁移待执行，但 API/worker 或外部客户端仍使用数据库 | 生成维护范围，先停已知消费者；外部影响不可处理时阻塞 |
| 新建环境缺少 uv venv、pnpm 依赖或构建产物 | 检查报告真实缺口；显式环境准备/构建任务，不在 read-only 检查中安装 |
| 无数据库锁适配器，多主机同时迁移 | 明确不受支持或阻止该模式，不宣称本机锁足够 |
| 字母序与依赖顺序相反、选择项排序变化 | 上游先显示，操作始终绑定稳定 node ID |
| v1 工程升级二进制 | 原注册可读取，不重复迁移、不改变控制范围 |

本草案只定义协议、调度与使用场景。它没有修改已安装程序、真实注册文件或数据库。

## 参考与验证边界

Docker 官方把启动、健康和一次性成功完成区分为不同依赖条件，本方案据此保留条件，避免“容器已启动”替代应用可用。[Compose 启动顺序](https://docs.docker.com/compose/how-tos/startup-order/)。

Alembic 官方介绍了在新库初始化时结合 create_all 与 stamp 的显式流程；这不意味着可对本次已有业务数据、结构与版本不一致的数据库自动 stamp。本方案的自动启动不做这种推断。[Alembic Cookbook](https://alembic.sqlalchemy.org/en/latest/cookbook.html#building-an-up-to-date-database-from-scratch)。

当前诊断依据本地源码、真实 PostgreSQL 只读元数据与容器日志；目标协议是本项目的设计决策。字段示例需要在实现阶段通过严格 schema 和真实适配器验收，文档中列出的场景不是已通过的测试。
