# codex-utils

[English](README_EN.md) | 简体中文

[![CI](https://github.com/chensunlai/codex-utils/actions/workflows/ci.yml/badge.svg)](https://github.com/chensunlai/codex-utils/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/chensunlai/codex-utils)](https://github.com/chensunlai/codex-utils/releases/latest)
[![License](https://img.shields.io/github/license/chensunlai/codex-utils)](LICENSE)

`codex-utils` 是一个 Codex 本地工具箱。目前提供历史数据修补功能：将 `config.toml` 中正在使用的 `model_provider` 和 `model` 同步到历史数据库、会话 JSONL 与全局索引。

程序默认打开键盘操作的终端界面，同时提供适合脚本和自动化的子命令。Release 是独立二进制文件，Windows CMD、PowerShell、Linux/Ubuntu 和 macOS 用户不需要安装 Go、Python 或其他运行时。

## 一键安装并运行

### Linux / Ubuntu / macOS

```bash
curl -fsSL https://raw.githubusercontent.com/chensunlai/codex-utils/main/scripts/install.sh | sh && "$HOME/.local/bin/codex-utils"
```

安装位置默认为 `~/.local/bin/codex-utils`。以后直接运行：

```bash
codex-utils
```

如果 `~/.local/bin` 尚未加入 `PATH`，也可以继续使用完整路径，或将下面一行加入 shell 配置：

```bash
export PATH="$HOME/.local/bin:$PATH"
```

### Windows PowerShell

```powershell
irm https://raw.githubusercontent.com/chensunlai/codex-utils/main/scripts/install.ps1 | iex; codex-utils
```

安装器会把 `%LOCALAPPDATA%\codex-utils\bin` 加入当前进程和用户 `PATH`。

### Windows CMD

```cmd
powershell -NoProfile -ExecutionPolicy Bypass -Command "irm 'https://raw.githubusercontent.com/chensunlai/codex-utils/main/scripts/install.ps1' | iex; codex-utils"
```

安装脚本会根据系统和 CPU 架构下载对应 Release，并用 `checksums.txt` 校验 SHA-256。

## TUI 操作

直接运行 `codex-utils` 后可使用：

| 按键 | 操作 |
| --- | --- |
| `↑` / `↓` 或 `k` / `j` | 移动选择 |
| `Enter` | 执行所选操作 |
| `y` / `n` | 确认或取消写入操作 |
| `Esc` | 返回上一级 |
| `q` | 退出 |

主界面包含状态检查、试运行、正式修补、手动备份和选择备份恢复。正式修补前一定会先创建备份。

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

## 固定版本或自定义安装目录

Linux / macOS：

```bash
curl -fsSL https://raw.githubusercontent.com/chensunlai/codex-utils/main/scripts/install.sh | \
  CODEX_UTILS_VERSION=v0.1.0 CODEX_UTILS_INSTALL_DIR="$HOME/bin" sh
```

通过管道执行时，环境变量需要导出才能传入子进程：

```bash
export CODEX_UTILS_VERSION=v0.1.0
export CODEX_UTILS_INSTALL_DIR="$HOME/bin"
curl -fsSL https://raw.githubusercontent.com/chensunlai/codex-utils/main/scripts/install.sh | sh
```

PowerShell：

```powershell
$env:CODEX_UTILS_VERSION = "v0.1.0"
$env:CODEX_UTILS_INSTALL_DIR = "$HOME\bin"
irm https://raw.githubusercontent.com/chensunlai/codex-utils/main/scripts/install.ps1 | iex
```

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
