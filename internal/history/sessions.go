package history

import (
	"bufio"
	"encoding/json"
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

// Session describes a conversation that can be transferred to another machine.
type Session struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	UpdatedAt    string `json:"updated_at"`
	Cwd          string `json:"cwd,omitempty"`
	ForkedFromID string `json:"forked_from_id,omitempty"`
	HistoryMode  string `json:"history_mode,omitempty"`
}

type historyBase struct {
	ThreadID            string `json:"thread_id"`
	EndByteOffset       int64  `json:"end_byte_offset"`
	EndOrdinalExclusive int64  `json:"end_ordinal_exclusive"`
}

type rolloutHeader struct {
	Type    string `json:"type"`
	Ordinal int64  `json:"ordinal"`
	Payload struct {
		ID           string          `json:"id"`
		Timestamp    string          `json:"timestamp"`
		Cwd          string          `json:"cwd"`
		Provider     string          `json:"model_provider"`
		Model        string          `json:"model"`
		Source       json.RawMessage `json:"source"`
		CLIVersion   string          `json:"cli_version"`
		ForkedFromID string          `json:"forked_from_id"`
		HistoryMode  string          `json:"history_mode"`
		HistoryBase  *historyBase    `json:"history_base"`
	} `json:"payload"`
}

type sessionFile struct {
	Path     string
	Relative string
	Size     int64
	Modified time.Time
	Header   rolloutHeader
}

type sessionRecord struct {
	Session
	Files   []*sessionFile
	Primary *sessionFile
	Index   map[string]any
}

func ListSessions(paths Paths) ([]Session, error) {
	catalog, err := readSessionCatalog(paths)
	if err != nil {
		return nil, err
	}
	sessions := make([]Session, 0, len(catalog))
	for _, record := range catalog {
		sessions = append(sessions, record.Session)
	}
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].UpdatedAt == sessions[j].UpdatedAt {
			return sessions[i].ID < sessions[j].ID
		}
		return sessions[i].UpdatedAt > sessions[j].UpdatedAt
	})
	return sessions, nil
}

func readSessionCatalog(paths Paths) (map[string]*sessionRecord, error) {
	catalog := make(map[string]*sessionRecord)
	for _, root := range []string{paths.SessionsDir, filepath.Join(paths.Home, "archived_sessions")} {
		if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, err
		}
		err := filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.Type().IsRegular() || !strings.HasPrefix(entry.Name(), "rollout-") || !strings.HasSuffix(entry.Name(), ".jsonl") {
				return nil
			}
			file, err := inspectSessionFile(paths.Home, filePath)
			if errors.Is(err, errNotSession) {
				return nil
			}
			if err != nil {
				return err
			}
			id := file.Header.Payload.ID
			record := catalog[id]
			if record == nil {
				record = &sessionRecord{Session: Session{ID: id, Title: id}}
				catalog[id] = record
			}
			record.Files = append(record.Files, file)
			if record.Primary == nil || file.Modified.After(record.Primary.Modified) {
				record.Primary = file
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("scan conversations: %w", err)
		}
	}
	index, _, err := readSessionIndex(paths.SessionIndex)
	if err != nil {
		return nil, err
	}
	threads, err := readThreadRecords(paths.StateDB)
	if err != nil {
		return nil, err
	}
	for id, record := range catalog {
		row := threads[id]
		for _, file := range record.Files {
			if portableBase(valueString(row["rollout_path"])) == filepath.Base(file.Path) {
				record.Primary = file
			}
		}
		meta := record.Primary.Header.Payload
		record.Cwd = meta.Cwd
		record.HistoryMode = meta.HistoryMode
		record.ForkedFromID = meta.ForkedFromID
		record.UpdatedAt = record.Primary.Modified.UTC().Format(time.RFC3339Nano)
		record.Index = cloneMap(index[id])
		if title := valueString(record.Index["thread_name"]); title != "" {
			record.Title = title
		} else if title := databaseString(row["title"]); title != "" {
			record.Title = title
		}
		if timestamp, ok := unixTimestamp(row["updated_at"]); ok {
			record.UpdatedAt = timestamp.UTC().Format(time.RFC3339Nano)
		}
		if cwd := databaseString(row["cwd"]); cwd != "" {
			record.Cwd = cwd
		}
		sort.Slice(record.Files, func(i, j int) bool { return record.Files[i].Relative < record.Files[j].Relative })
	}
	return catalog, nil
}

var errNotSession = errors.New("not a session rollout")

func inspectSessionFile(home, filePath string) (*sessionFile, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	line, err := bufio.NewReader(file).ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	var header rolloutHeader
	if json.Unmarshal(line, &header) != nil || header.Type != "session_meta" || strings.TrimSpace(header.Payload.ID) == "" {
		return nil, errNotSession
	}
	relative, err := filepath.Rel(home, filePath)
	if err != nil {
		return nil, err
	}
	return &sessionFile{Path: filePath, Relative: filepath.ToSlash(relative), Size: info.Size(), Modified: info.ModTime(), Header: header}, nil
}

func readSessionIndex(filePath string) (map[string]map[string]any, []byte, error) {
	raw, err := os.ReadFile(filePath)
	if errors.Is(err, os.ErrNotExist) {
		return make(map[string]map[string]any), nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	index := make(map[string]map[string]any)
	for _, line := range splitLines(raw) {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) == nil {
			if id := valueString(record["id"]); id != "" {
				index[id] = record
			}
		}
	}
	return index, raw, nil
}

func portableBase(value string) string {
	return path.Base(strings.ReplaceAll(value, `\`, "/"))
}
