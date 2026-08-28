package history

import (
	"fmt"
	"os"
)

type Inspection struct {
	Paths         Paths
	Settings      ModelSettings
	ConfigFound   bool
	DatabaseFound bool
	SessionsFound bool
	IndexFound    bool
	BackupCount   int
}

func Inspect(paths Paths) (Inspection, error) {
	settings, err := LoadModelSettings(paths.Config)
	if err != nil {
		return Inspection{}, err
	}
	backups, err := ListBackups(paths)
	if err != nil {
		return Inspection{}, err
	}
	return Inspection{
		Paths:         paths,
		Settings:      settings,
		ConfigFound:   fileExists(paths.Config),
		DatabaseFound: fileExists(paths.StateDB),
		SessionsFound: fileExists(paths.SessionsDir),
		IndexFound:    fileExists(paths.SessionIndex),
		BackupCount:   len(backups),
	}, nil
}

func Sync(paths Paths, settings ModelSettings, dryRun bool) (Stats, error) {
	if info, err := os.Stat(paths.Home); err != nil || !info.IsDir() {
		return Stats{}, fmt.Errorf("Codex home does not exist: %s", paths.Home)
	}
	stats := Stats{}
	if !dryRun {
		backupPath, err := CreateBackup(paths)
		if err != nil {
			return stats, err
		}
		stats.BackupPath = backupPath
	}
	if err := syncStateDatabase(paths, settings, &stats, dryRun); err != nil {
		return stats, err
	}
	if err := syncRolloutFiles(paths, settings, &stats, dryRun); err != nil {
		return stats, err
	}
	if err := syncSessionIndex(paths, settings, &stats, dryRun); err != nil {
		return stats, err
	}
	return stats, nil
}
