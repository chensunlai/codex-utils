package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chensunlai/codex-utils/internal/buildinfo"
	"github.com/chensunlai/codex-utils/internal/history"
	"github.com/chensunlai/codex-utils/internal/tui"
	"golang.org/x/term"
)

const usage = `codex-utils repairs Codex history model metadata.

Usage:
  codex-utils                              Open the interactive TUI
  codex-utils status                       Show detected files and settings
  codex-utils sync [--dry-run]             Synchronize history metadata
  codex-utils preview                      Preview synchronization
  codex-utils backup                       Create a backup archive
  codex-utils list-backups                 List backup archives
  codex-utils restore <path|latest>         Restore a backup archive
  codex-utils version                      Print version information

Global options:
  --codex-home <path>                      Override CODEX_HOME (default ~/.codex)
  -h, --help                               Show this help
`

func Run(args []string) int {
	home, remaining, err := parseGlobalOptions(args)
	if err != nil {
		return fail(err)
	}
	paths, err := history.ResolvePaths(home)
	if err != nil {
		return fail(err)
	}
	if len(remaining) == 0 {
		if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
			fmt.Print(usage)
			return 0
		}
		if err := tui.Run(paths); err != nil {
			return fail(err)
		}
		return 0
	}

	command := remaining[0]
	commandArgs := remaining[1:]
	switch command {
	case "help", "-h", "--help":
		fmt.Print(usage)
		return 0
	case "version", "--version", "-v":
		fmt.Printf("codex-utils %s (commit %s, built %s)\n", buildinfo.Version, buildinfo.Commit, buildinfo.Date)
		return 0
	case "status":
		if len(commandArgs) != 0 {
			return fail(fmt.Errorf("status does not accept arguments"))
		}
		return runStatus(paths)
	case "preview":
		if len(commandArgs) != 0 {
			return fail(fmt.Errorf("preview does not accept arguments"))
		}
		return runSync(paths, true)
	case "sync", "repair":
		flags := flag.NewFlagSet(command, flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		dryRun := flags.Bool("dry-run", false, "show what would change without writing")
		if err := flags.Parse(commandArgs); err != nil {
			return 2
		}
		if flags.NArg() != 0 {
			return fail(fmt.Errorf("unexpected argument: %s", flags.Arg(0)))
		}
		return runSync(paths, *dryRun)
	case "backup":
		if len(commandArgs) != 0 {
			return fail(fmt.Errorf("backup does not accept arguments"))
		}
		backup, err := history.CreateBackup(paths)
		if err != nil {
			return fail(err)
		}
		fmt.Printf("Backup: %s\n", backup)
		return 0
	case "list-backups", "backups":
		if len(commandArgs) != 0 {
			return fail(fmt.Errorf("list-backups does not accept arguments"))
		}
		backups, err := history.ListBackups(paths)
		if err != nil {
			return fail(err)
		}
		if len(backups) == 0 {
			fmt.Println("No backups found.")
			return 0
		}
		for _, backup := range backups {
			fmt.Println(backup)
		}
		return 0
	case "restore":
		if len(commandArgs) != 1 {
			return fail(fmt.Errorf("restore requires one backup path or 'latest'"))
		}
		backup := commandArgs[0]
		if backup == "latest" {
			backups, err := history.ListBackups(paths)
			if err != nil {
				return fail(err)
			}
			if len(backups) == 0 {
				return fail(fmt.Errorf("no backups found"))
			}
			backup = backups[len(backups)-1]
		}
		if err := history.RestoreBackup(paths, backup); err != nil {
			return fail(err)
		}
		absolute, _ := filepath.Abs(backup)
		fmt.Printf("Restored: %s\n", absolute)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown command %q\n\n%s", command, usage)
		return 2
	}
}

func parseGlobalOptions(args []string) (string, []string, error) {
	var home string
	remaining := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		argument := args[index]
		switch {
		case argument == "--codex-home":
			if index+1 >= len(args) {
				return "", nil, fmt.Errorf("--codex-home requires a path")
			}
			index++
			home = args[index]
		case strings.HasPrefix(argument, "--codex-home="):
			home = strings.TrimPrefix(argument, "--codex-home=")
			if home == "" {
				return "", nil, fmt.Errorf("--codex-home requires a path")
			}
		default:
			remaining = append(remaining, argument)
		}
	}
	return home, remaining, nil
}

func runStatus(paths history.Paths) int {
	inspection, err := history.Inspect(paths)
	if err != nil {
		return fail(err)
	}
	fmt.Printf("Codex home:      %s\n", inspection.Paths.Home)
	fmt.Printf("Config:          %s (%s)\n", inspection.Paths.Config, found(inspection.ConfigFound))
	fmt.Printf("State database:  %s (%s)\n", inspection.Paths.StateDB, found(inspection.DatabaseFound))
	fmt.Printf("Sessions dir:    %s (%s)\n", inspection.Paths.SessionsDir, found(inspection.SessionsFound))
	fmt.Printf("Session index:   %s (%s)\n", inspection.Paths.SessionIndex, found(inspection.IndexFound))
	fmt.Printf("Model provider:  %s\n", inspection.Settings.Provider)
	fmt.Printf("Model:           %s\n", inspection.Settings.Model)
	fmt.Printf("Backups:         %d\n", inspection.BackupCount)
	return 0
}

func runSync(paths history.Paths, dryRun bool) int {
	settings, err := history.LoadModelSettings(paths.Config)
	if err != nil {
		return fail(err)
	}
	stats, err := history.Sync(paths, settings, dryRun)
	if err != nil {
		return fail(err)
	}
	mode := "Synced"
	if dryRun {
		mode = "Dry run"
	}
	fmt.Printf("%s: provider=%s, model=%s\n", mode, settings.Provider, settings.Model)
	fmt.Printf("Database threads: %d/%d updated\n", stats.DBThreadsUpdated, stats.DBThreadsSeen)
	fmt.Printf("Rollout files:    %d/%d updated\n", stats.RolloutFilesUpdated, stats.RolloutFilesSeen)
	fmt.Printf("Index rows:       %d/%d updated\n", stats.IndexRowsUpdated, stats.IndexRowsSeen)
	if stats.MalformedJSONLines > 0 {
		fmt.Printf("Malformed index lines skipped: %d\n", stats.MalformedJSONLines)
	}
	if stats.BackupPath != "" {
		fmt.Printf("Backup: %s\n", stats.BackupPath)
	}
	if dryRun && !stats.Changed() {
		fmt.Println("No changes needed.")
	}
	return 0
}

func found(value bool) string {
	if value {
		return "found"
	}
	return "missing"
}

func fail(err error) int {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	return 2
}
