# codex-utils

English | [简体中文](README.md)

[![CI](https://github.com/chensunlai/codex-utils/actions/workflows/ci.yml/badge.svg)](https://github.com/chensunlai/codex-utils/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/chensunlai/codex-utils)](https://github.com/chensunlai/codex-utils/releases/latest)
[![License](https://img.shields.io/github/license/chensunlai/codex-utils)](LICENSE)

`codex-utils` is a local Codex toolkit. Its first utility repairs history metadata by synchronizing the active `model_provider` and `model` from `config.toml` into the history database, rollout JSONL files, and global session index.

The standalone release binaries run in Windows CMD, PowerShell, Linux/Ubuntu, and macOS without Go, Python, or another runtime. Running the command without arguments opens an interactive terminal UI; subcommands are available for scripts.

## Run temporarily

### Linux / macOS

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

The command detects the OS and CPU architecture, verifies the release against `checksums.txt`, and opens the TUI. Downloads stay in the system temporary directory and are deleted when the tool exits. It does not update `PATH` or leave a program file behind.

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
