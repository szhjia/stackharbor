# 协议 v2

当前可执行契约、字段与能力边界见 [协议 v2 完整参考](../skills/stackharbor/references/v2.md)。

原设计：[协议与编排草案](protocol-v2-proposal.md)。
验证：[实施记录](verification/protocol-v2.md)。

## Compose 多文件资源

协议 v2 支持有序 `files`、可选 `project_directory` 和 `env_files`，兼容原有 `file`（两者不能同时使用）。路径相对 workspace 文件，默认工作目录是首个 Compose 文件所在目录，不自动加入 override。验证使用官方解析器，需要 Docker CLI 与 Compose 插件，解析本身不需要引擎运行。配置变化后容器操作会被拒绝，需重开工作区并重新规划。详见[字段及示例](../skills/stackharbor/references/v2.md#ordered-compose-inputs)。
