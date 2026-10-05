package history

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const sessionArchiveFormat = "codex-utils.sessions"

type archiveSession struct {
	Session
	Files   []string       `json:"files"`
	Primary string         `json:"primary"`
	Index   map[string]any `json:"index,omitempty"`
	Meta    rolloutHeader  `json:"-"`
}

type archiveFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type sessionManifest struct {
	Format   string           `json:"format"`
	Version  int              `json:"version"`
	Created  string           `json:"created_at"`
	Selected []string         `json:"selected_ids"`
	Sessions []archiveSession `json:"sessions"`
	Files    []archiveFile    `json:"files"`
}

type ExportStats struct {
	Path         string
	Selected     int
	Dependencies int
	Files        int
}

type ImportStats struct {
	Added        int
	Skipped      int
	Adapted      int
	Dependencies int
}

func ExportSessions(paths Paths, ids []string, output string) (stats ExportStats, retErr error) {
	if len(ids) == 0 {
		return stats, fmt.Errorf("select at least one conversation")
	}
	catalog, err := readSessionCatalog(paths)
	if err != nil {
		return stats, err
	}
	selected := make(map[string]bool)
	for _, id := range ids {
		if catalog[id] == nil {
			return stats, fmt.Errorf("conversation not found: %s", id)
		}
		selected[id] = true
	}
	included, err := collectSessionDependencies(catalog, selected)
	if err != nil {
		return stats, err
	}
	output, err = expandPath(output)
	if err != nil {
		return stats, err
	}
	if _, err := os.Lstat(output); err == nil {
		return stats, fmt.Errorf("output already exists: %s", output)
	} else if !errors.Is(err, os.ErrNotExist) {
		return stats, err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return stats, err
	}
	temporary, err := os.CreateTemp(filepath.Dir(output), ".codex-sessions-*.zip")
	if err != nil {
		return stats, err
	}
	defer func() { _ = temporary.Close(); _ = os.Remove(temporary.Name()) }()
	stage, err := os.MkdirTemp("", "codex-sessions-export-*")
	if err != nil {
		return stats, err
	}
	defer os.RemoveAll(stage)
	writer := zip.NewWriter(temporary)
	manifest := sessionManifest{Format: sessionArchiveFormat, Version: 1, Created: time.Now().UTC().Format(time.RFC3339Nano)}
	for id := range selected {
		manifest.Selected = append(manifest.Selected, id)
	}
	sort.Strings(manifest.Selected)
	var allIDs []string
	for id := range included {
		allIDs = append(allIDs, id)
	}
	sort.Strings(allIDs)
	for _, id := range allIDs {
		record := catalog[id]
		session := archiveSession{Session: record.Session, Primary: record.Primary.Relative, Index: record.Index}
		for _, file := range record.Files {
			if !validRolloutArchivePath(file.Relative) {
				return stats, fmt.Errorf("non-portable rollout path: %s", file.Relative)
			}
			entry, err := addSessionZipFile(writer, file.Path, file.Relative, file.Size)
			if err != nil {
				return stats, err
			}
			manifest.Files = append(manifest.Files, entry)
			session.Files = append(session.Files, file.Relative)
			stats.Files++
		}
		manifest.Sessions = append(manifest.Sessions, session)
	}
	for _, name := range []string{StateDBName, historyDBName} {
		source := filepath.Join(paths.Home, name)
		if !fileExists(source) {
			continue
		}
		snapshot := filepath.Join(stage, name)
		if err := createTransferDatabase(source, snapshot, name, allIDs); err != nil {
			return stats, fmt.Errorf("export %s: %w", name, err)
		}
		info, err := os.Stat(snapshot)
		if err != nil {
			return stats, err
		}
		entry, err := addSessionZipFile(writer, snapshot, "databases/"+name, info.Size())
		if err != nil {
			return stats, err
		}
		manifest.Files = append(manifest.Files, entry)
	}
	// Appended or rewritten rollouts would make the database snapshot and fork
	// byte offsets disagree. Ask the user to retry after Codex has stopped.
	for _, id := range allIDs {
		for _, file := range catalog[id].Files {
			info, err := os.Stat(file.Path)
			if err != nil || info.Size() != file.Size || !info.ModTime().Equal(file.Modified) {
				return stats, fmt.Errorf("conversation changed during export; close Codex and retry: %s", id)
			}
		}
	}
	entry, err := writer.Create("manifest.json")
	if err != nil {
		return stats, err
	}
	if err := json.NewEncoder(entry).Encode(manifest); err != nil {
		return stats, err
	}
	if err := writer.Close(); err != nil {
		return stats, err
	}
	if err := temporary.Sync(); err != nil {
		return stats, err
	}
	if err := temporary.Close(); err != nil {
		return stats, err
	}
	input, err := os.Open(temporary.Name())
	if err != nil {
		return stats, err
	}
	defer input.Close()
	if err := writeNewFile(output, input); err != nil {
		return stats, err
	}
	stats.Path = output
	stats.Selected = len(selected)
	stats.Dependencies = len(included) - len(selected)
	return stats, nil
}

func addSessionZipFile(writer *zip.Writer, source, name string, size int64) (archiveFile, error) {
	input, err := os.Open(source)
	if err != nil {
		return archiveFile{}, err
	}
	defer input.Close()
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetMode(0o600)
	output, err := writer.CreateHeader(header)
	if err != nil {
		return archiveFile{}, err
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(output, hash), io.LimitReader(input, size))
	if err != nil || written != size {
		return archiveFile{}, fmt.Errorf("read archive member %s: expected %d bytes, got %d (%v)", name, size, written, err)
	}
	return archiveFile{Path: name, Size: size, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func collectSessionDependencies(catalog map[string]*sessionRecord, selected map[string]bool) (map[string]bool, error) {
	included := make(map[string]bool)
	var queue []string
	for id := range selected {
		queue = append(queue, id)
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if included[id] {
			continue
		}
		record := catalog[id]
		if record == nil {
			return nil, fmt.Errorf("missing fork history dependency: %s", id)
		}
		included[id] = true
		for _, file := range record.Files {
			base := file.Header.Payload.HistoryBase
			if base == nil {
				continue
			}
			if file.Header.Ordinal != base.EndOrdinalExclusive {
				return nil, fmt.Errorf("invalid history ordinal for %s: %s", id, file.Relative)
			}
			parent := historyDependencyRecord(catalog, base)
			if parent == nil {
				return nil, fmt.Errorf("missing or invalid history prefix for %s (dependency %s)", id, base.ThreadID)
			}
			queue = append(queue, parent.ID)
		}
	}
	return included, nil
}

func historyDependencyRecord(catalog map[string]*sessionRecord, base *historyBase) *sessionRecord {
	if record := catalog[base.ThreadID]; record != nil && hasHistoryBoundary(historyReferenceFiles(record.Files, base.ThreadID), base) {
		return record
	}
	// A rotated rollout can have its own file ID while session_meta.id still
	// identifies the original conversation. history_base may reference that
	// segment ID, which is stored as the final UUID in the filename.
	for _, record := range catalog {
		for _, file := range record.Files {
			stem := strings.TrimSuffix(filepath.Base(file.Path), ".jsonl")
			if len(stem) >= 36 && stem[len(stem)-36:] == base.ThreadID && hasHistoryBoundary([]*sessionFile{file}, base) {
				return record
			}
		}
	}
	return nil
}

func historyReferenceFiles(files []*sessionFile, id string) []*sessionFile {
	var exact []*sessionFile
	for _, file := range files {
		stem := strings.TrimSuffix(filepath.Base(file.Path), ".jsonl")
		if strings.HasSuffix(stem, "-"+id) || strings.HasSuffix(stem, "_"+id) {
			exact = append(exact, file)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return files
}

func hasHistoryBoundary(files []*sessionFile, base *historyBase) bool {
	if base.ThreadID == "" || base.EndByteOffset <= 0 || base.EndOrdinalExclusive <= 0 {
		return false
	}
	for _, file := range files {
		if file.Header.Ordinal >= base.EndOrdinalExclusive {
			continue
		}
		input, err := os.Open(file.Path)
		if err != nil {
			continue
		}
		matched := historyBoundaryMatches(input, base)
		if !matched {
			// Metadata may have been rewritten since this byte position was
			// recorded. Check the ordinal as well: exporting complete files
			// must preserve existing history rather than reinterpret it.
			_, _ = input.Seek(0, io.SeekStart)
			reader := bufio.NewReader(input)
			for {
				line, err := reader.ReadBytes('\n')
				var record struct {
					Ordinal *int64 `json:"ordinal"`
				}
				if json.Unmarshal(line, &record) == nil && record.Ordinal != nil {
					if *record.Ordinal == base.EndOrdinalExclusive-1 {
						matched = true
						break
					}
					if *record.Ordinal >= base.EndOrdinalExclusive {
						break
					}
				}
				if err != nil {
					break
				}
			}
		}
		_ = input.Close()
		if matched {
			return true
		}
	}
	return false
}

func historyBoundaryMatches(file *os.File, base *historyBase) bool {
	// Read backwards to the last complete record before the byte boundary.
	end := base.EndByteOffset
	var suffix []byte
	for end > 0 {
		start := end - 64*1024
		if start < 0 {
			start = 0
		}
		chunk := make([]byte, end-start)
		if _, err := file.ReadAt(chunk, start); err != nil {
			return false
		}
		suffix = append(chunk, suffix...)
		if suffix[len(suffix)-1] != '\n' {
			return false
		}
		line := bytes.TrimSuffix(suffix, []byte{'\n'})
		separator := bytes.LastIndexByte(line, '\n')
		if separator >= 0 || start == 0 {
			var record struct {
				Ordinal *int64 `json:"ordinal"`
			}
			if json.Unmarshal(line[separator+1:], &record) != nil {
				return false
			}
			return record.Ordinal != nil && *record.Ordinal == base.EndOrdinalExclusive-1
		}
		end = start
	}
	return false
}

func validRolloutArchivePath(name string) bool {
	if strings.ContainsAny(name, `\:`) || path.IsAbs(name) || path.Clean(name) != name {
		return false
	}
	parts := strings.Split(name, "/")
	if len(parts) < 2 || (parts[0] != "sessions" && parts[0] != "archived_sessions") {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || strings.ContainsAny(part, `<>"|?*`) {
			return false
		}
		for _, character := range part {
			if character < 32 {
				return false
			}
		}
		device := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if device == "CON" || device == "PRN" || device == "AUX" || device == "NUL" || (len(device) == 4 && (strings.HasPrefix(device, "COM") || strings.HasPrefix(device, "LPT")) && device[3] >= '1' && device[3] <= '9') {
			return false
		}
	}
	return strings.HasPrefix(path.Base(name), "rollout-") && strings.HasSuffix(name, ".jsonl")
}

func importedRolloutPath(paths Paths, name string) string {
	if strings.HasPrefix(name, "archived_sessions/") {
		name = "sessions/imported/" + strings.TrimPrefix(name, "archived_sessions/")
	}
	return filepath.Join(paths.Home, filepath.FromSlash(name))
}

func readSessionArchive(archivePath, stage string) (sessionManifest, error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return sessionManifest{}, err
	}
	defer reader.Close()
	members := make(map[string]*zip.File)
	for _, file := range reader.File {
		if file.Name != "manifest.json" && file.Name != "databases/"+StateDBName && file.Name != "databases/"+historyDBName && !validRolloutArchivePath(file.Name) {
			return sessionManifest{}, fmt.Errorf("unsupported ZIP member: %s", file.Name)
		}
		if !file.Mode().IsRegular() || members[file.Name] != nil {
			return sessionManifest{}, fmt.Errorf("non-regular or duplicate ZIP member: %s", file.Name)
		}
		members[file.Name] = file
	}
	file := members["manifest.json"]
	if file == nil || file.UncompressedSize64 > 32*1024*1024 {
		return sessionManifest{}, fmt.Errorf("missing or oversized session manifest")
	}
	input, err := file.Open()
	if err != nil {
		return sessionManifest{}, err
	}
	raw, readErr := io.ReadAll(io.LimitReader(input, 32*1024*1024+1))
	_ = input.Close()
	if readErr != nil {
		return sessionManifest{}, readErr
	}
	var manifest sessionManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return manifest, err
	}
	if manifest.Format != sessionArchiveFormat || manifest.Version != 1 || len(manifest.Selected) == 0 || len(manifest.Sessions) == 0 {
		return manifest, fmt.Errorf("unsupported session archive format or version")
	}
	declared := map[string]bool{"manifest.json": true}
	for _, entry := range manifest.Files {
		file := members[entry.Path]
		if file == nil || declared[entry.Path] || entry.Size < 0 || uint64(entry.Size) != file.UncompressedSize64 {
			return manifest, fmt.Errorf("invalid manifest member: %s", entry.Path)
		}
		declared[entry.Path] = true
		input, err := file.Open()
		if err != nil {
			return manifest, err
		}
		hash := sha256.New()
		target := filepath.Join(stage, filepath.FromSlash(entry.Path))
		err = writeNewFile(target, io.TeeReader(input, hash))
		_ = input.Close()
		if err != nil {
			return manifest, err
		}
		if hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
			return manifest, fmt.Errorf("checksum mismatch: %s", entry.Path)
		}
	}
	if len(declared) != len(members) {
		return manifest, fmt.Errorf("ZIP contains undeclared members")
	}
	ids := make(map[string]bool)
	used := make(map[string]bool)
	catalog := make(map[string]*sessionRecord)
	for i := range manifest.Sessions {
		session := &manifest.Sessions[i]
		if session.ID == "" || ids[session.ID] || len(session.Files) == 0 {
			return manifest, fmt.Errorf("duplicate or empty conversation in manifest")
		}
		ids[session.ID] = true
		record := &sessionRecord{Session: session.Session}
		catalog[session.ID] = record
		for _, name := range session.Files {
			if !validRolloutArchivePath(name) || !declared[name] || used[name] {
				return manifest, fmt.Errorf("invalid conversation file: %s", name)
			}
			used[name] = true
			file, err := inspectSessionFile(stage, filepath.Join(stage, filepath.FromSlash(name)))
			if err != nil || file.Header.Payload.ID != session.ID {
				return manifest, fmt.Errorf("rollout does not match conversation %s: %s", session.ID, name)
			}
			record.Files = append(record.Files, file)
			if name == session.Primary {
				session.Meta = file.Header
				record.Primary = file
			}
		}
		if record.Primary == nil {
			return manifest, fmt.Errorf("missing primary rollout for %s", session.ID)
		}
	}
	selected := make(map[string]bool)
	for _, id := range manifest.Selected {
		if !ids[id] || selected[id] {
			return manifest, fmt.Errorf("invalid selected conversation: %s", id)
		}
		selected[id] = true
	}
	for _, entry := range manifest.Files {
		if validRolloutArchivePath(entry.Path) && !used[entry.Path] {
			return manifest, fmt.Errorf("unassigned rollout: %s", entry.Path)
		}
	}
	included, err := collectSessionDependencies(catalog, selected)
	if err != nil {
		return manifest, err
	}
	if len(included) != len(ids) {
		return manifest, fmt.Errorf("archive includes unrelated conversations")
	}
	for _, name := range []string{StateDBName, historyDBName} {
		filePath := filepath.Join(stage, "databases", name)
		if fileExists(filePath) {
			if err := validateTransferDatabase(filePath, name, ids); err != nil {
				return manifest, err
			}
		}
	}
	return manifest, nil
}

// ImportSessions adds conversations. Identical conversations are skipped;
// conflicting IDs abort the import rather than replacing local history.
func ImportSessions(paths Paths, archivePath, cwd string) (stats ImportStats, retErr error) {
	archivePath, err := expandPath(archivePath)
	if err != nil {
		return stats, err
	}
	if cwd == "" {
		cwd = "~"
	}
	cwd, err = expandPath(cwd)
	if err != nil {
		return stats, err
	}
	stage, err := os.MkdirTemp("", "codex-sessions-import-*")
	if err != nil {
		return stats, err
	}
	defer os.RemoveAll(stage)
	manifest, err := readSessionArchive(archivePath, stage)
	if err != nil {
		return stats, err
	}
	settings, err := LoadModelSettings(paths.Config)
	if err != nil {
		return stats, err
	}
	catalog, err := readSessionCatalog(paths)
	if err != nil {
		return stats, err
	}
	threads, err := readThreadRecords(paths.StateDB)
	if err != nil {
		return stats, err
	}
	checksums := make(map[string]archiveFile)
	for _, file := range manifest.Files {
		checksums[file.Path] = file
	}
	primary := make(map[string]string)
	register := make(map[string]archiveSession)
	var existingFiles []*sessionFile
	var additions []string
	type originalLocation struct {
		Header rolloutHeader
		Length int
	}
	originalLocations := make(map[string]originalLocation)
	for _, session := range manifest.Sessions {
		for _, name := range session.Files {
			header, err := readSessionHeader(filepath.Join(stage, filepath.FromSlash(name)))
			if err != nil {
				return stats, err
			}
			var meta rolloutHeader
			if err := json.Unmarshal(header, &meta); err != nil {
				return stats, err
			}
			originalLocations[name] = originalLocation{meta, len(header)}
		}
	}
	for _, session := range manifest.Sessions {
		existing := catalog[session.ID]
		if existing != nil {
			if !sameSessionFiles(existing.Files, session.Files, checksums, stage) {
				return ImportStats{}, fmt.Errorf("conversation %s already exists with different history; no conversations were imported", session.ID)
			}
			primary[session.ID] = existing.Primary.Path
			existingFiles = append(existingFiles, existing.Files...)
			for _, file := range existing.Files {
				header, err := readSessionHeader(file.Path)
				if err != nil {
					return stats, err
				}
				for _, name := range session.Files {
					if path.Base(name) == filepath.Base(file.Path) {
						if err := replaceSessionHeader(filepath.Join(stage, filepath.FromSlash(name)), header); err != nil {
							return stats, err
						}
					}
				}
			}
			stats.Skipped++
		} else {
			if threads[session.ID] != nil {
				return ImportStats{}, fmt.Errorf("conversation %s already exists in the destination database", session.ID)
			}
			for _, name := range session.Files {
				target := importedRolloutPath(paths, name)
				if err := ensureNoChildSymlinks(paths.Home, target); err != nil {
					return ImportStats{}, err
				}
				if _, err := os.Lstat(target); err == nil {
					return ImportStats{}, fmt.Errorf("destination rollout already exists: %s", target)
				} else if !errors.Is(err, os.ErrNotExist) {
					return ImportStats{}, err
				}
				additions = append(additions, name)
			}
			primary[session.ID] = importedRolloutPath(paths, session.Primary)
			stats.Added++
		}
		register[session.ID] = session
	}
	reindex, err := relocateSessionCwd(stage, manifest, cwd)
	if err != nil {
		return stats, err
	}
	for _, session := range manifest.Sessions {
		for _, name := range session.Files {
			header, err := readSessionHeader(filepath.Join(stage, filepath.FromSlash(name)))
			if err != nil {
				return stats, err
			}
			var meta rolloutHeader
			if err := json.Unmarshal(header, &meta); err != nil {
				return stats, err
			}
			original := originalLocations[name]
			baseBefore, _ := json.Marshal(original.Header.Payload.HistoryBase)
			baseAfter, _ := json.Marshal(meta.Payload.HistoryBase)
			if original.Length != len(header) || !bytes.Equal(baseBefore, baseAfter) {
				reindex[session.ID] = true
			}
		}
	}
	stats.Dependencies = len(manifest.Sessions) - len(manifest.Selected)
	index, originalIndex, err := readSessionIndex(paths.SessionIndex)
	if err != nil {
		return ImportStats{}, err
	}
	indexExisted := fileExists(paths.SessionIndex)
	desiredIndex := append([]byte(nil), originalIndex...)
	for _, session := range manifest.Sessions {
		if index[session.ID] != nil && cwd == "" {
			continue
		}
		row := cloneMap(session.Index)
		if index[session.ID] != nil {
			row = cloneMap(index[session.ID])
		}
		row["id"] = session.ID
		if valueString(row["thread_name"]) == "" {
			row["thread_name"] = session.Title
		}
		row["updated_at"] = session.UpdatedAt
		row["rollout_path"] = primary[session.ID]
		row["cwd"] = session.Cwd
		row["model_provider"] = settings.Provider
		if cwd != "" {
			row["cwd"] = cwd
		}
		encoded, err := marshalCompactJSON(row)
		if err != nil {
			return ImportStats{}, err
		}
		if len(desiredIndex) > 0 && desiredIndex[len(desiredIndex)-1] != '\n' {
			desiredIndex = append(desiredIndex, '\n')
		}
		desiredIndex = append(desiredIndex, encoded...)
		desiredIndex = append(desiredIndex, '\n')
	}
	if err := ensureNoChildSymlinks(paths.Home, paths.SessionIndex); err != nil {
		return ImportStats{}, err
	}
	var created []string
	var newDatabases []string
	originalFiles := make(map[string]string)
	indexChanged := false
	defer func() {
		if retErr == nil {
			return
		}
		for filePath, backup := range originalFiles {
			file, err := os.Open(backup)
			if err == nil {
				err = atomicWriteReader(filePath, file, existingMode(filePath))
				_ = file.Close()
			}
			retErr = errors.Join(retErr, err)
		}
		if indexChanged {
			if indexExisted {
				retErr = errors.Join(retErr, atomicWriteFile(paths.SessionIndex, originalIndex, existingMode(paths.SessionIndex)))
			} else {
				retErr = errors.Join(retErr, os.Remove(paths.SessionIndex))
			}
		}
		for _, file := range created {
			retErr = errors.Join(retErr, os.Remove(file))
		}
		for _, file := range newDatabases {
			_ = os.Remove(file)
			_ = os.Remove(file + "-wal")
			_ = os.Remove(file + "-shm")
			_ = os.Remove(file + "-journal")
		}
		stats = ImportStats{}
	}()
	control, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return stats, err
	}
	control.SetMaxOpenConns(1)
	defer control.Close()
	if _, err := control.Exec("PRAGMA busy_timeout = 30000"); err != nil {
		return stats, err
	}
	attached := make(map[string]string)
	for i, name := range []string{StateDBName, historyDBName} {
		target := filepath.Join(paths.Home, name)
		if err := ensureNoChildSymlinks(paths.Home, target); err != nil {
			return stats, err
		}
		source := filepath.Join(stage, "databases", name)
		if !fileExists(target) && fileExists(source) {
			if err := bootstrapTransferDatabase(source, target, name); err != nil {
				return stats, err
			}
			newDatabases = append(newDatabases, target)
		}
		if fileExists(target) {
			schema := []string{"state", "history"}[i]
			if _, err := control.Exec("ATTACH DATABASE ? AS "+schema, target); err != nil {
				return stats, err
			}
			attached[name] = schema
		}
	}
	transaction, err := control.Begin()
	if err != nil {
		return stats, err
	}
	defer transaction.Rollback()
	for _, name := range []string{StateDBName, historyDBName} {
		if schema := attached[name]; schema != "" {
			source := filepath.Join(stage, "databases", name)
			if !fileExists(source) {
				source = ""
			}
			if err := mergeTransferDatabase(transaction, schema, source, name, register, primary, cwd, settings.Provider, reindex); err != nil {
				return stats, err
			}
		}
	}
	adapted := make(map[string]bool)
	for _, session := range manifest.Sessions {
		for _, name := range session.Files {
			changed, err := adaptSessionProvider(filepath.Join(stage, filepath.FromSlash(name)), settings.Provider)
			if err != nil {
				return stats, err
			}
			if changed {
				adapted[session.ID] = true
			}
		}
	}
	for _, file := range existingFiles {
		var stagedPath string
		for _, name := range register[file.Header.Payload.ID].Files {
			if path.Base(name) == filepath.Base(file.Path) {
				stagedPath = filepath.Join(stage, filepath.FromSlash(name))
				break
			}
		}
		before, err := readSessionHeader(file.Path)
		if err != nil {
			return stats, err
		}
		after, err := readSessionHeader(stagedPath)
		if err != nil {
			return stats, err
		}
		if bytes.Equal(before, after) && sameFileContents(file.Path, stagedPath) {
			continue
		}
		input, err := os.Open(file.Path)
		if err != nil {
			return stats, err
		}
		backup := filepath.Join(stage, "originals", filepath.Base(file.Path))
		err = writeNewFile(backup, input)
		_ = input.Close()
		if err != nil {
			return stats, err
		}
		originalFiles[file.Path] = backup
		staged, err := os.Open(stagedPath)
		if err != nil {
			return stats, err
		}
		err = atomicWriteReader(file.Path, staged, existingMode(file.Path))
		_ = staged.Close()
		if err != nil {
			return stats, err
		}
	}
	for _, name := range additions {
		stagedPath := filepath.Join(stage, filepath.FromSlash(name))
		input, err := os.Open(stagedPath)
		if err != nil {
			return stats, err
		}
		target := importedRolloutPath(paths, name)
		err = writeNewFile(target, input)
		_ = input.Close()
		if err != nil {
			return stats, err
		}
		created = append(created, target)
	}
	if !bytes.Equal(desiredIndex, originalIndex) {
		if err := atomicWriteFile(paths.SessionIndex, desiredIndex, existingMode(paths.SessionIndex)); err != nil {
			return stats, err
		}
		indexChanged = true
	}
	if err := transaction.Commit(); err != nil {
		return stats, err
	}
	stats.Adapted = len(adapted)
	return stats, nil
}

func sameSessionFiles(existing []*sessionFile, names []string, checksums map[string]archiveFile, stage string) bool {
	if len(existing) != len(names) {
		return false
	}
	byName := make(map[string]*sessionFile)
	for _, file := range existing {
		byName[filepath.Base(file.Path)] = file
	}
	for _, name := range names {
		file := byName[path.Base(name)]
		entry := checksums[name]
		if file == nil {
			return false
		}
		input, err := os.Open(file.Path)
		if err != nil {
			return false
		}
		hash := sha256.New()
		_, err = io.Copy(hash, input)
		_ = input.Close()
		if err != nil {
			return false
		}
		if hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 && !sameSessionHistory(file.Path, filepath.Join(stage, filepath.FromSlash(name))) {
			return false
		}
	}
	return true
}

func sameSessionHistory(first, second string) bool {
	left, err := os.Open(first)
	if err != nil {
		return false
	}
	defer left.Close()
	right, err := os.Open(second)
	if err != nil {
		return false
	}
	defer right.Close()
	a, b := bufio.NewReader(left), bufio.NewReader(right)
	for {
		firstLine, firstErr := a.ReadBytes('\n')
		secondLine, secondErr := b.ReadBytes('\n')
		if firstErr != nil && firstErr != io.EOF || secondErr != nil && secondErr != io.EOF {
			return false
		}
		if !bytes.Equal(firstLine, secondLine) {
			firstMeta, firstOK := portableSessionMetadata(firstLine)
			secondMeta, secondOK := portableSessionMetadata(secondLine)
			if !firstOK || !secondOK || !bytes.Equal(firstMeta, secondMeta) {
				return false
			}
		}
		if firstErr == io.EOF || secondErr == io.EOF {
			return firstErr == secondErr
		}
	}
}

// Only runtime location metadata may differ after importing. Messages, tools,
// other settings, history ownership and ordinal cutoffs must still match.
func portableSessionMetadata(line []byte) ([]byte, bool) {
	var record map[string]any
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.UseNumber()
	if decoder.Decode(&record) != nil {
		return nil, false
	}
	payload, _ := record["payload"].(map[string]any)
	if payload == nil {
		return nil, false
	}
	switch record["type"] {
	case "session_meta":
		delete(payload, "model_provider")
		if base, ok := payload["history_base"].(map[string]any); ok {
			delete(base, "end_byte_offset")
		}
	case "turn_context":
	case "event_msg":
		if payload["type"] != "thread_settings_applied" {
			return nil, false
		}
		payload, _ = payload["thread_settings"].(map[string]any)
		if payload == nil {
			return nil, false
		}
	default:
		return nil, false
	}
	for _, key := range []string{"cwd", "runtime_workspace_roots", "workspace_roots"} {
		delete(payload, key)
	}
	encoded, err := marshalCompactJSON(record)
	return encoded, err == nil
}

func sameFileContents(first, second string) bool {
	var hashes [][]byte
	for _, name := range []string{first, second} {
		file, err := os.Open(name)
		if err != nil {
			return false
		}
		hash := sha256.New()
		_, err = io.Copy(hash, file)
		_ = file.Close()
		if err != nil {
			return false
		}
		hashes = append(hashes, hash.Sum(nil))
	}
	return bytes.Equal(hashes[0], hashes[1])
}

func writeNewFile(target string, input io.Reader) (retErr error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		_ = output.Close()
		if retErr != nil {
			_ = os.Remove(target)
		}
	}()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	return output.Close()
}
