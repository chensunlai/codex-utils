# codex-utils

[English](README_EN.md) | 简体中文

[![CI](https://github.com/chensunlai/codex-utils/actions/workflows/ci.yml/badge.svg)](https://github.com/chensunlai/codex-utils/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/chensunlai/codex-utils)](https://github.com/chensunlai/codex-utils/releases/latest)
[![License](https://img.shields.io/github/license/chensunlai/codex-utils)](LICENSE)

`codex-utils` 是一个 Codex 本地工具箱，提供历史数据修补和对话迁移功能：同步模型元数据，或将指定对话打包为 ZIP，在其他机器上追加恢复。

程序默认打开键盘操作的终端界面，同时提供适合脚本和自动化的子命令。Release 是独立二进制文件，Windows CMD、PowerShell、Linux/Ubuntu 和 macOS 用户不需要安装 Go、Python 或其他运行时。

## 一键临时运行

### Linux / Ubuntu / macOS

```bash
curl -fsSL https://raw.githubusercontent.com/chensunlai/codex-utils/main/scripts/run.sh | sh
```

### Windows PowerShell

```powershell
iex (irm 'https://raw.githubusercontent.com/chensunlai/codex-utils/main/scripts/run.ps1')
```

### Windows CMD

```cmd
powershell -NoProfile -ExecutionPolicy Bypass -Command "iex (irm 'https://raw.githubusercontent.com/chensunlai/codex-utils/main/scripts/run.ps1')"
```

命令会根据系统和 CPU 架构下载对应 Release、校验 SHA-256，然后直接进入 TUI。所有下载内容只保存在系统临时目录，退出工具后自动删除，不写入 `PATH`，不保留程序文件。

## TUI 操作

启动时先选择 **简体中文** 或 **English**。进入主菜单后可使用：

| 按键 | 操作 |
| --- | --- |
| `↑` / `↓` 或 `k` / `j` | 移动选择 |
| `Enter` | 执行所选操作 |
| `y` / `n` | 确认或取消写入操作 |
| `Esc` | 返回上一级 |
| `q` | 退出 |

主界面包含状态检查、试运行、正式修补、手动备份和选择备份恢复。正式修补前一定会先创建备份。

主菜单还提供 **导出对话** 和 **导入对话**。导出列表默认隐藏 Guardian review 等内部审查和子代理会话，长标题显示为单行。按 `Space` 勾选多个对话，`a` 全选或清空，`Enter` 输入 ZIP 保存路径；未勾选时会导出当前选中的对话。导入时输入 ZIP 路径，可选填写本机工作目录（留空默认使用本机 `~`），再确认添加。路径输入支持粘贴、退格和 `Ctrl+u` 清空。

## 对话归档与跨机恢复

先关闭正在使用这些对话的 Codex 进程，再导出或导入。

```bash
# 查看会话 ID 和标题
codex-utils list-sessions

# 导出一个对话
codex-utils export -o conversation.zip <session-id>

# 将多个对话放入同一个 ZIP
codex-utils export -o conversations.zip <session-id-1> <session-id-2>
```

将 ZIP 手动复制到另一台 Windows、Linux 或 macOS 机器，然后运行：

```bash
# 向目标机器添加对话
codex-utils import conversations.zip

# 可选：指定目标数据目录，以及这些对话在本机对应的项目目录
codex-utils --codex-home /path/to/.codex import --cwd /path/to/project conversations.zip
```

Windows PowerShell 中也可以使用本机路径：

```powershell
codex-utils import --cwd 'D:\Projects\my-project' '.\conversations.zip'
```

ZIP 包含所选对话的全部 rollout 文件、索引元数据以及对应的 SQLite 会话记录。分页历史的 `history_base` 依赖会递归收集，因此导出 fork 时，包内也会包含其依赖源会话的完整历史和分段文件。导入时这些依赖会一起添加；结果会显示依赖数量。

导入会适配本机配置的 provider，避免跨机器恢复后被 Codex 的 provider 筛选隐藏。旧 provider 从会话头部移除时以等长空白填充，保留字节偏移。数据库和索引中的 rollout 路径会转换为目标机器的路径；默认 workspace 和工作目录均为本机 `~`，可通过 `--cwd` 指定其他本机目录。会话头部的 `runtime_workspace_roots`、运行设置快照以及 turn context 中的路径会一并转换，避免 Windows 路径在其他系统上阻止恢复。数据库和索引也会同步更新。头部长度变化时会按被引用的分段文件调整 fork 字节偏移，并在重新打开对话时由 Codex 重建受影响的历史索引。用户和助手消息正文、工具记录及 ordinal 不变；仅转换运行路径元数据；已归档的源会话导入后会放入活动会话目录。

导入采用追加方式：已有会话的历史相同时不会重复添加，但会修复 provider、工作目录或缺失的历史轮次索引；同 ID 的历史内容不同时，会拒绝整次导入，不覆盖本机记录。导出也不会覆盖已有 ZIP，且不会删除源会话。使用 v0.2.0 导入后找不到对话时，升级后重新导入同一 ZIP 即可修复；导入后重新启动 Codex，再打开对话。若本机项目路径不同，可在重新导入时指定 `--cwd`。

ZIP 不包含 `config.toml`、登录凭据、项目文件或外部附件。目标 Codex 应使用相同版本或支持源会话历史格式的版本。导入前会校验所有 ZIP 成员、SHA-256、数据库和 fork 依赖；不接受越界路径、链接或未声明的文件。

## 修补内容

Codex 历史元数据通常位于：

- `~/.codex/config.toml`
- `~/.codex/state_5.sqlite`
- `~/.codex/sessions/**/rollout-*.jsonl`
- `~/.codex/session_index.jsonl`

本工具会执行以下操作：

1. 从 `config.toml` 读取当前 `model_provider` 和 `model`。
2. 更新 SQLite `threads` 表中不一致的模型字段。
3. 更新每个 rollout 首行 `session_meta` 的模型字段，其他事件行保持不变。
4. 根据数据库补全或重建 `session_index.jsonl`，保留已有自定义字段以及 `cwd`、Git 分支、提交哈希、远端地址和 rollout 路径。
5. 在任何正式同步前，将可能修改的文件打包到 `~/.codex/history-sync-backups/`。

JSONL 使用同目录临时文件原子替换。恢复前会完整校验归档成员，拒绝绝对路径、`..`、Windows 盘符、符号链接和其他非普通文件。

## 命令行用法

建议先退出正在运行的 Codex，再执行正式修补。

```text
codex-utils                              打开交互式 TUI
codex-utils status                       查看路径和当前模型设置
codex-utils preview                      试运行，不写入文件
codex-utils sync --dry-run               与 preview 等价
codex-utils sync                         创建备份并修补历史数据
codex-utils backup                       只创建备份
codex-utils list-backups                 列出备份
codex-utils restore latest               恢复最新备份
codex-utils restore <backup.tar.gz>       恢复指定备份
codex-utils list-sessions                 列出会话 ID 和标题
codex-utils export -o <zip> <id> [id...]   导出所选对话及 fork 历史依赖
codex-utils import [--cwd <path>] <zip>   从 ZIP 追加恢复对话
codex-utils version                      查看版本
```

在无 TTY 的脚本环境中直接运行而不带参数时，程序只输出帮助，不会修改数据。

### 指定 Codex 数据目录

所有子命令都支持全局参数：

```bash
codex-utils --codex-home /path/to/.codex status
codex-utils --codex-home /path/to/.codex sync --dry-run
```

也可以设置环境变量：

```bash
export CODEX_HOME=/path/to/.codex
codex-utils
```

PowerShell：

```powershell
$env:CODEX_HOME = "$env:USERPROFILE\.codex"
codex-utils
```

如果配置文件不存在，扫描会使用保守默认值 `openai` / `gpt-5`，并在状态页明确显示。

## 手动下载

[Releases](https://github.com/chensunlai/codex-utils/releases/latest) 提供以下文件：

| 系统 | x86-64 | ARM64 |
| --- | --- | --- |
| Linux / Ubuntu | `codex-utils_linux_amd64.tar.gz` | `codex-utils_linux_arm64.tar.gz` |
| macOS | `codex-utils_darwin_amd64.tar.gz` | `codex-utils_darwin_arm64.tar.gz` |
| Windows | `codex-utils_windows_amd64.zip` | `codex-utils_windows_arm64.zip` |

每个 Release 同时提供 `checksums.txt`。

## 开发

需要 Go 1.25+：

```bash
go test ./...
go vet ./...
go build -trimpath -o bin/codex-utils ./cmd/codex-utils
```

生成所有发布包：

```bash
./scripts/build-release.sh dev dist
```

推送 `v*` 标签会运行测试、构建六个平台包、生成校验和并发布 GitHub Release。

## License

[MIT](LICENSE)
