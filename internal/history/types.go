package history

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	DefaultProvider  = "openai"
	DefaultModel     = "gpt-5"
	StateDBName      = "state_5.sqlite"
	SessionIndexName = "session_index.jsonl"
	BackupDirName    = "history-sync-backups"
)

type Paths struct {
	Home         string
	Config       string
	StateDB      string
	SessionsDir  string
	SessionIndex string
	BackupDir    string
}

type ModelSettings struct {
	Provider string
	Model    string
}

type Stats struct {
	DBThreadsSeen       int
	DBThreadsUpdated    int
	RolloutFilesSeen    int
	RolloutFilesUpdated int
	IndexRowsSeen       int
	IndexRowsUpdated    int
	MalformedJSONLines  int
	BackupPath          string
}

func (s Stats) Changed() bool {
	return s.DBThreadsUpdated > 0 || s.RolloutFilesUpdated > 0 || s.IndexRowsUpdated > 0
}

func ResolvePaths(homeOverride string) (Paths, error) {
	home := strings.TrimSpace(homeOverride)
	if home == "" {
		home = strings.TrimSpace(os.Getenv("CODEX_HOME"))
	}
	if home == "" {
		home = "~/.codex"
	}

	expanded, err := expandPath(home)
	if err != nil {
		return Paths{}, err
	}
	return Paths{
		Home:         expanded,
		Config:       filepath.Join(expanded, "config.toml"),
		StateDB:      filepath.Join(expanded, StateDBName),
		SessionsDir:  filepath.Join(expanded, "sessions"),
		SessionIndex: filepath.Join(expanded, SessionIndexName),
		BackupDir:    filepath.Join(expanded, BackupDirName),
	}, nil
}

func expandPath(value string) (string, error) {
	value = os.ExpandEnv(value)
	if value == "~" || strings.HasPrefix(value, "~/") || strings.HasPrefix(value, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home: %w", err)
		}
		if value == "~" {
			value = home
		} else {
			value = filepath.Join(home, value[2:])
		}
	}
	abs, err := filepath.Abs(filepath.Clean(value))
	if err != nil {
		return "", fmt.Errorf("resolve Codex home %q: %w", value, err)
	}
	return abs, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
