# 开发热更新验收补充

2026-10-27 整理。原始 2026-10-18 本机排查记录包含个人路径及其他项目的历史状态，已保留为本机证据；本文仅描述本仓库可复现的开发契约。

StackHarbor skill 0.4.1 和 fallback watcher 已包含在提交 f8ab19a。本批未提交改动补充 README、开发说明及真实 Vite HMR 集成测试，不重复引入 watcher 功能。

## 接入与验收

- 优先登记项目原生的前台开发命令。一次性构建、preview 和既有容器镜像不自动获得源码监听。
- 缺少原生监听时使用随 skill 提供的 `scripts/dev_watch.py` 模板和项目内 JSON 配置；不依赖个人目录中的 skill 路径。
- Go 构建失败保留上一版进程，恢复后替换；Python 在新进程中重新导入。源码变更不隐式执行迁移。
- 退出时回收本次创建的进程组及后代；不停止观察对象，不改动既有业务容器和数据卷。
- `started` 仅表示进程存活。业务就绪需要独立探针；既有容器的健康响应不能证明新源码已加载。

## 本仓库验证入口

`make check` 运行 watcher 的 Python 回归测试、Go 测试与 race 检查。设置 `STACKHARBOR_TEST_VITE` 为已安装 Vite CLI 的绝对路径，可启用 `internal/runner/hmr_integration_test.go`：通过实际 runner 启动临时 Vite，编辑源码，并检查 HMR WebSocket 更新及新内容。测试只使用临时目录和本机随机端口。

完整接入步骤见 [热更新排查](../skills/stackharbor/references/hot-reload.md)，验证要求与可选环境见 [开发说明](development.md)。这些验证不代替各接入项目的业务或数据库验收，也不代表发布、推送或部署。
