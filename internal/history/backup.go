package history

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func CreateBackup(paths Paths) (backupPath string, retErr error) {
	if info, err := os.Stat(paths.Home); err != nil || !info.IsDir() {
		return "", fmt.Errorf("Codex home does not exist: %s", paths.Home)
	}
	if err := os.MkdirAll(paths.BackupDir, 0o755); err != nil {
		return "", fmt.Errorf("create backup directory: %w", err)
	}
	candidates, err := backupCandidates(paths)
	if err != nil {
		return "", err
	}

	base := "codex-history-" + time.Now().Format("20060102-150405")
	backupPath = filepath.Join(paths.BackupDir, base+".tar.gz")
	for suffix := 1; fileExists(backupPath); suffix++ {
		backupPath = filepath.Join(paths.BackupDir, fmt.Sprintf("%s-%d.tar.gz", base, suffix))
	}

	temp, err := os.CreateTemp(paths.BackupDir, ".codex-history-*.tar.gz")
	if err != nil {
		return "", fmt.Errorf("create backup: %w", err)
	}
	tempName := temp.Name()
	defer func() {
		_ = temp.Close()
		if retErr != nil {
			_ = os.Remove(tempName)
		}
	}()

	gzipWriter := gzip.NewWriter(temp)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, candidate := range candidates {
		if err := addBackupFile(tarWriter, paths.Home, candidate); err != nil {
			return "", err
		}
	}
	if err := tarWriter.Close(); err != nil {
		return "", fmt.Errorf("finish backup archive: %w", err)
	}
	if err := gzipWriter.Close(); err != nil {
		return "", fmt.Errorf("finish compressed backup: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return "", fmt.Errorf("sync backup: %w", err)
	}
	if err := temp.Close(); err != nil {
		return "", fmt.Errorf("close backup: %w", err)
	}
	if err := replaceFile(tempName, backupPath); err != nil {
		return "", fmt.Errorf("publish backup: %w", err)
	}
	return backupPath, nil
}

func backupCandidates(paths Paths) ([]string, error) {
	candidates := make([]string, 0, 3)
	for _, candidate := range []string{paths.Config, paths.StateDB, paths.SessionIndex} {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			candidates = append(candidates, candidate)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("inspect %s: %w", candidate, err)
		}
	}
	if fileExists(paths.SessionsDir) {
		err := filepath.WalkDir(paths.SessionsDir, func(filePath string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			name := entry.Name()
			if strings.HasPrefix(name, "rollout-") && strings.HasSuffix(name, ".jsonl") {
				info, err := os.Stat(filePath)
				if err != nil {
					return err
				}
				if info.Mode().IsRegular() {
					candidates = append(candidates, filePath)
				}
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("scan sessions for backup: %w", err)
		}
	}
	sort.Strings(candidates)
	return candidates, nil
}

func addBackupFile(writer *tar.Writer, home, filePath string) error {
	info, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("inspect backup file %s: %w", filePath, err)
	}
	relative, err := filepath.Rel(home, filePath)
	if err != nil {
		return fmt.Errorf("resolve backup path: %w", err)
	}
	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return fmt.Errorf("create backup header: %w", err)
	}
	header.Name = filepath.ToSlash(relative)
	header.Format = tar.FormatPAX
	if err := writer.WriteHeader(header); err != nil {
		return fmt.Errorf("write backup header: %w", err)
	}
	input, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open backup file %s: %w", filePath, err)
	}
	_, copyErr := io.Copy(writer, input)
	closeErr := input.Close()
	if copyErr != nil {
		return fmt.Errorf("archive %s: %w", filePath, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close %s: %w", filePath, closeErr)
	}
	return nil
}

func ListBackups(paths Paths) ([]string, error) {
	backups, err := filepath.Glob(filepath.Join(paths.BackupDir, "codex-history-*.tar.gz"))
	if err != nil {
		return nil, fmt.Errorf("list backups: %w", err)
	}
	sort.Strings(backups)
	return backups, nil
}

func RestoreBackup(paths Paths, backupPath string) error {
	expanded, err := expandPath(backupPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(expanded); err != nil {
		return fmt.Errorf("backup not found: %s", expanded)
	}
	if err := inspectArchive(expanded, paths.Home); err != nil {
		return err
	}
	return extractArchive(expanded, paths.Home)
}

func inspectArchive(archivePath, home string) error {
	return walkArchive(archivePath, func(header *tar.Header, _ *tar.Reader) error {
		switch header.Typeflag {
		case tar.TypeReg, tar.TypeRegA, tar.TypeDir:
		default:
			return fmt.Errorf("backup contains unsupported member %q", header.Name)
		}
		_, err := safeRestoreTarget(home, header.Name)
		return err
	})
}

func extractArchive(archivePath, home string) error {
	return walkArchive(archivePath, func(header *tar.Header, reader *tar.Reader) error {
		target, err := safeRestoreTarget(home, header.Name)
		if err != nil {
			return err
		}
		mode := os.FileMode(header.Mode).Perm()
		if header.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(target, mode); err != nil {
				return fmt.Errorf("restore directory %s: %w", target, err)
			}
			return nil
		}
		if err := ensureNoChildSymlinks(home, target); err != nil {
			return err
		}
		if err := atomicWriteReader(target, io.LimitReader(reader, header.Size), mode); err != nil {
			return fmt.Errorf("restore %s: %w", target, err)
		}
		return nil
	})
}

func walkArchive(archivePath string, visit func(*tar.Header, *tar.Reader) error) error {
	input, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	defer input.Close()
	gzipReader, err := gzip.NewReader(input)
	if err != nil {
		return fmt.Errorf("backup is not a valid tar.gz archive: %w", err)
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read backup archive: %w", err)
		}
		if err := visit(header, reader); err != nil {
			return err
		}
	}
}

func safeRestoreTarget(home, memberName string) (string, error) {
	normalized := strings.ReplaceAll(memberName, `\`, "/")
	if normalized == "" || path.IsAbs(normalized) {
		return "", fmt.Errorf("backup member escapes CODEX_HOME: %s", memberName)
	}
	parts := strings.Split(normalized, "/")
	if len(parts) > 0 && len(parts[0]) == 2 && parts[0][1] == ':' {
		return "", fmt.Errorf("backup member escapes CODEX_HOME: %s", memberName)
	}
	for _, part := range parts {
		if part == ".." {
			return "", fmt.Errorf("backup member escapes CODEX_HOME: %s", memberName)
		}
	}
	cleaned := path.Clean(normalized)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("backup member escapes CODEX_HOME: %s", memberName)
	}
	homeAbs, err := filepath.Abs(home)
	if err != nil {
		return "", err
	}
	target := filepath.Join(homeAbs, filepath.FromSlash(cleaned))
	relative, err := filepath.Rel(homeAbs, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("backup member escapes CODEX_HOME: %s", memberName)
	}
	return target, nil
}

func ensureNoChildSymlinks(home, target string) error {
	relative, err := filepath.Rel(home, target)
	if err != nil {
		return err
	}
	current := home
	parts := strings.Split(relative, string(filepath.Separator))
	for _, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("backup member traverses symlink: %s", current)
		}
	}
	return nil
}
