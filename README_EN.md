# codex-utils

English | [简体中文](README.md)

[![CI](https://github.com/chensunlai/codex-utils/actions/workflows/ci.yml/badge.svg)](https://github.com/chensunlai/codex-utils/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/chensunlai/codex-utils)](https://github.com/chensunlai/codex-utils/releases/latest)
[![License](https://img.shields.io/github/license/chensunlai/codex-utils)](LICENSE)

`codex-utils` is a local Codex toolkit. Its first utility repairs history metadata by synchronizing the active `model_provider` and `model` from `config.toml` into the history database, rollout JSONL files, and global session index.

The standalone release binaries run in Windows CMD, PowerShell, Linux/Ubuntu, and macOS without Go, Python, or another runtime. Running the command without arguments opens an interactive terminal UI; subcommands are available for scripts.

## Install and run

### Linux / macOS

```bash
curl -fsSL https://raw.githubusercontent.com/chensunlai/codex-utils/main/scripts/install.sh | sh && "$HOME/.local/bin/codex-utils"
```

The default installation path is `~/.local/bin/codex-utils`.

### Windows PowerShell

```powershell
irm https://raw.githubusercontent.com/chensunlai/codex-utils/main/scripts/install.ps1 | iex; codex-utils
```

### Windows CMD

```cmd
powershell -NoProfile -ExecutionPolicy Bypass -Command "irm 'https://raw.githubusercontent.com/chensunlai/codex-utils/main/scripts/install.ps1' | iex; codex-utils"
```

Both installers detect the OS and CPU architecture and verify the downloaded release against `checksums.txt` before installing it.

## TUI controls

| Key | Action |
| --- | --- |
| `Up` / `Down` or `k` / `j` | Move selection |
| `Enter` | Run the selected action |
| `y` / `n` | Confirm or cancel a write operation |
| `Esc` | Go back |
| `q` | Quit |

The main menu provides status inspection, a dry-run preview, history repair, manual backup, and backup restore. A repair always creates a backup first.

## What it repairs

Codex history metadata is normally stored in:

- `~/.codex/config.toml`
- `~/.codex/state_5.sqlite`
- `~/.codex/sessions/**/rollout-*.jsonl`
- `~/.codex/session_index.jsonl`

The tool reads the active model settings, updates inconsistent rows in the SQLite `threads` table, updates only the first `session_meta` record in each rollout, and completes or rebuilds the session index. Existing custom fields and working directory, Git branch, commit, remote URL, and rollout path metadata are preserved.

Before a real synchronization, affected files are archived under `~/.codex/history-sync-backups/`. JSONL changes use atomic replacement. Restore validates every archive member before extraction and rejects absolute paths, traversal, drive-prefixed paths, links, and non-regular members.

## CLI reference

Close running Codex processes before a real repair.

```text
codex-utils                              Open the interactive TUI
codex-utils status                       Show paths and model settings
codex-utils preview                      Scan without writing
codex-utils sync --dry-run               Same as preview
codex-utils sync                         Back up and repair history
codex-utils backup                       Create a backup only
codex-utils list-backups                 List backups
codex-utils restore latest               Restore the newest backup
codex-utils restore <backup.tar.gz>       Restore a selected backup
codex-utils version                      Show version information
```

Override the data directory with either form:

```bash
codex-utils --codex-home /path/to/.codex status
CODEX_HOME=/path/to/.codex codex-utils preview
```

When `config.toml` is missing, inspection uses conservative `openai` / `gpt-5` defaults and displays them explicitly.

## Pinning a version

The installers accept `CODEX_UTILS_VERSION` and `CODEX_UTILS_INSTALL_DIR`.

```bash
export CODEX_UTILS_VERSION=v0.1.0
export CODEX_UTILS_INSTALL_DIR="$HOME/bin"
curl -fsSL https://raw.githubusercontent.com/chensunlai/codex-utils/main/scripts/install.sh | sh
```

```powershell
$env:CODEX_UTILS_VERSION = "v0.1.0"
$env:CODEX_UTILS_INSTALL_DIR = "$HOME\bin"
irm https://raw.githubusercontent.com/chensunlai/codex-utils/main/scripts/install.ps1 | iex
```

## Release files

[Releases](https://github.com/chensunlai/codex-utils/releases/latest) contain `amd64` and `arm64` archives for Linux, macOS, and Windows, plus SHA-256 checksums.

## Development

Go 1.25 or newer is required:

```bash
go test ./...
go vet ./...
go build -trimpath -o bin/codex-utils ./cmd/codex-utils
```

Build all release archives with `./scripts/build-release.sh dev dist`. Pushing a `v*` tag runs tests and publishes a GitHub Release.

## License

[MIT](LICENSE)
