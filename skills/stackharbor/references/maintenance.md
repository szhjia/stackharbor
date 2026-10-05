# 安装、升级和维护

技能的权威实现是 StackHarbor 仓库的 `skills/stackharbor/`，与 CLI、解析器和协议参考在同一次变更中维护。其它工程的注册文件只依赖 StackHarbor 命令和本工程路径，不引用工具源码的绝对路径。

## 用户级安装

从公开 GitHub 仓库安装：`npx skills add szhjia/stackharbor --skill stackharbor -g`。该方式下载完整技能目录，后续用 `npx skills update stackharbor -g` 只更新此技能；升级前检查本机是否有自定义改动。skills.sh 展示仓库技能，页面索引可能滞后于源码更新。以下源码链接安装是另一种入口。

在源码根目录或**永久保留**的发行解压目录执行：

```sh
sh scripts/install-skill.sh
```

默认创建 `~/.agents/skills/stackharbor` → 当前源目录 `skills/stackharbor` 的绝对软链接。安装不需要二进制，不安装业务依赖。整个目录的相对参考链接保持有效。重复安装同一来源成功；已有目录、文件、其它来源软链接（含失效链接）均拒绝覆盖。先核实已有内容，再由用户决定是否替换。

Codex 和兼容的 agent 通过 `~/.agents/skills` 发现用户技能。如果实际运行环境只读取专用目录，可传入该目录，例如：

```sh
sh scripts/install-skill.sh "$HOME/.codex/skills"
```

选择实际使用的入口即可，避免维护多个复制实现。安装后用 `readlink "$HOME/.agents/skills/stackharbor"` 与 `test -r "$HOME/.agents/skills/stackharbor/SKILL.md"` 检查，再开启新会话。触发示例：“使用 stackharbor 技能，将当前工程接入栈港。”

## 升级

源码更新后，链接立即读取新版技能和参考，不需重新复制；已打开的 agent 会话可能需要重新加载。**二进制不会因技能链接自动更新**：另行按发行说明更新二进制，并核对 `stackharbor --version` 和 `--help`。使用明确的发行标签和校验清单；以实际命令及 validate/plan 结果核实能力。

发行安装：技能随发行包一起发布。解压到固定目录时，可链接该目录并在升级时更新其内容；使用按版本划分的解压目录时，需明确迁移技能入口到新目录。不要删除链接来源或链接临时解压目录。移动源码后，旧链接会失效，核实旧入口归属后重新安装到新来源；安装器不会自动替换它。

## 随工具维护

1. 改 YAML 字段、CLI、节点 ID、退出码或生命周期时，同步 `SKILL.md`、相应 `references/`、示例和回归检查。
2. 更新行为前运行基线应用场景，更新后让独立 agent 使用技能完成同一场景。特别检查已有 workspace 保留、缺少迁移契约时的处理、只读校验边界和升级路径。
3. `sh scripts/check.sh` 检查实际安装器、嵌入 YAML 的 validate/plan 和全部仓库测试；`sh scripts/build-release.sh VERSION` 把同一技能及安装器加入四个平台发行包。
4. 审核并提交相关文件；只按用户授权推送或发布。不要为其它工程复制另一份协议参考。
