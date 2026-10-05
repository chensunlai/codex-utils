# codex-utils

English | [简体中文](README.md)

[![CI](https://github.com/chensunlai/codex-utils/actions/workflows/ci.yml/badge.svg)](https://github.com/chensunlai/codex-utils/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/chensunlai/codex-utils)](https://github.com/chensunlai/codex-utils/releases/latest)
[![License](https://img.shields.io/github/license/chensunlai/codex-utils)](LICENSE)

`codex-utils` is a local Codex toolkit for repairing history metadata and transferring conversations. Synchronize model metadata, or pack selected conversations into a ZIP and add them on another machine.

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

The first screen lets you choose **简体中文** or **English**.

| Key | Action |
| --- | --- |
| `Up` / `Down` or `k` / `j` | Move selection |
| `Enter` | Run the selected action |
| `y` / `n` | Confirm or cancel a write operation |
| `Esc` | Go back |
| `q` | Quit |

The main menu provides status inspection, a dry-run preview, history repair, manual backup, and backup restore. A repair always creates a backup first.

**Export conversations** supports multiple selection: `Space` toggles a conversation, `a` selects all or clears the selection, and `Enter` opens the output ZIP path prompt. With no checked items, Enter exports the highlighted conversation. **Import conversations** asks for a ZIP path, an optional local working directory, and confirmation. Path prompts support paste, backspace, and `Ctrl+u` to clear.

## Transfer conversations between machines

Close Codex processes using the conversations before exporting or importing.

```bash
codex-utils list-sessions
codex-utils export -o conversation.zip <session-id>
codex-utils export -o conversations.zip <session-id-1> <session-id-2>
```

Manually copy the ZIP to another Windows, Linux, or macOS machine:

```bash
codex-utils import conversations.zip
codex-utils --codex-home /path/to/.codex import --cwd /path/to/project conversations.zip
```

The ZIP contains all rollout files for the selected conversations, their index metadata, and their SQLite conversation records. Paginated `history_base` dependencies are collected recursively. Exporting a fork also includes the complete source conversations and rollout segments it depends on; these conversations are added during import. The result reports the dependency count.

Import adapts conversations to the destination provider so Codex's provider filter can find them. Old provider metadata is removed with equal-length whitespace to retain byte offsets. Database and index rollout paths are rewritten for the destination machine. Workspace roots and the working directory default to the local home directory (`~`); `--cwd` overrides this default. Import relocates paths in rollout headers, saved runtime settings, turn contexts, database records, and index records to prevent foreign Windows paths from blocking restore. If a header grows, fork byte offsets are remapped to the referenced segment and Codex rebuilds affected history indexes when the conversation is reopened. Messages, tool records, and ordinals remain unchanged; only runtime location metadata is adapted. Archived source conversations are imported into the active sessions directory.

Import adds conversations: matching history is not duplicated, while provider metadata, working directories, and missing turn indexes can be repaired. Conflicting history under the same ID rejects the entire import without replacing local records. Export refuses an existing output ZIP and leaves the source conversations in place. If a v0.2.0 import is missing from Codex, upgrade and import the same ZIP again, then restart Codex and open the conversation. Pass `--cwd` if the destination project path differs. The export picker hides internal Guardian review and subagent threads by default and keeps long titles on one line.

Archives exclude configuration, login credentials, project files, and external attachments. Use the same Codex version or one that supports the source history format. Import validates all ZIP members, SHA-256 checksums, databases, and fork dependencies before adding data; traversal paths, links, and undeclared files are rejected.

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
codex-utils list-sessions                 List conversation IDs and titles
codex-utils export -o <zip> <id> [id...]   Export conversations and fork dependencies
codex-utils import [--cwd <path>] <zip>   Add conversations from an exported ZIP
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
