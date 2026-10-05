package history

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Relocate the session metadata and remap byte references before installing any
// files. Event records and ordinal boundaries are never changed.
func relocateSessionCwd(stage string, manifest sessionManifest, cwd string) error {
	catalog, err := readSessionCatalog(Paths{Home: stage, SessionsDir: filepath.Join(stage, "sessions"), SessionIndex: filepath.Join(stage, "session_index.jsonl"), StateDB: filepath.Join(stage, "state_5.sqlite")})
	if err != nil {
		return err
	}
	type reference struct {
		file   *sessionFile
		offset int64
	}
	parents := make(map[string]reference)
	for _, record := range catalog {
		for _, file := range record.Files {
			base := file.Header.Payload.HistoryBase
			if base == nil {
				continue
			}
			parent := historyDependencyRecord(catalog, base)
			if parent == nil {
				return fmt.Errorf("missing history dependency for %s", file.Relative)
			}
			for _, candidate := range historyReferenceFiles(parent.Files, base.ThreadID) {
				if candidate.Header.Ordinal >= base.EndOrdinalExclusive {
					continue
				}
				if offset, ok := ordinalByteBoundary(candidate.Path, base.EndOrdinalExclusive); ok {
					parents[file.Path] = reference{candidate, offset}
					break
				}
			}
			if parents[file.Path].file == nil {
				return fmt.Errorf("missing history boundary for %s", file.Relative)
			}
		}
	}
	deltas := make(map[string]int64)
	visiting := make(map[string]bool)
	var relocate func(*sessionFile) error
	relocate = func(file *sessionFile) error {
		if _, done := deltas[file.Path]; done {
			return nil
		}
		if visiting[file.Path] {
			return fmt.Errorf("cyclic history dependency: %s", file.Relative)
		}
		visiting[file.Path] = true
		parent := parents[file.Path]
		if parent.file != nil {
			if err := relocate(parent.file); err != nil {
				return err
			}
		}
		if (cwd == "" || file.Header.Payload.Cwd == cwd) && (parent.file == nil || file.Header.Payload.HistoryBase.EndByteOffset == parent.offset+deltas[parent.file.Path]) {
			deltas[file.Path] = 0
			return nil
		}
		header, err := readSessionHeader(file.Path)
		if err != nil {
			return err
		}
		var record map[string]any
		decoder := json.NewDecoder(bytes.NewReader(header))
		decoder.UseNumber()
		if err := decoder.Decode(&record); err != nil {
			return err
		}
		payload := record["payload"].(map[string]any)
		if cwd != "" {
			payload["cwd"] = cwd
		}
		if parent.file != nil {
			payload["history_base"].(map[string]any)["end_byte_offset"] = parent.offset + deltas[parent.file.Path]
		}
		encoded, err := marshalCompactJSON(record)
		if err != nil {
			return err
		}
		if padding := len(header) - len(encoded) - 1; padding > 0 {
			encoded = append(encoded, bytes.Repeat([]byte{' '}, padding)...)
		}
		encoded = append(encoded, '\n')
		deltas[file.Path] = int64(len(encoded) - len(header))
		return replaceSessionHeader(file.Path, encoded)
	}
	for _, session := range manifest.Sessions {
		for _, file := range catalog[session.ID].Files {
			if err := relocate(file); err != nil {
				return err
			}
		}
	}
	return nil
}

func ordinalByteBoundary(filePath string, endOrdinal int64) (int64, bool) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, false
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	var offset int64
	for {
		line, err := reader.ReadBytes('\n')
		offset += int64(len(line))
		var record struct {
			Ordinal *int64 `json:"ordinal"`
		}
		if json.Unmarshal(line, &record) == nil && record.Ordinal != nil && *record.Ordinal == endOrdinal-1 {
			return offset, true
		}
		if err != nil {
			return 0, false
		}
	}
}

func readSessionHeader(filePath string) ([]byte, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	header, err := bufio.NewReader(file).ReadBytes('\n')
	if err == io.EOF {
		err = nil
	}
	return header, err
}

func replaceSessionHeader(filePath string, header []byte) error {
	// Stage the complete stream, then close the original before replacing it
	// so this works on Windows too.
	input, err := os.Open(filePath)
	if err != nil {
		return err
	}
	reader := bufio.NewReader(input)
	_, err = reader.ReadBytes('\n')
	if err != nil && err != io.EOF {
		_ = input.Close()
		return err
	}
	temp, err := os.CreateTemp("", "codex-session-header-*")
	if err != nil {
		_ = input.Close()
		return err
	}
	defer os.Remove(temp.Name())
	_, err = io.Copy(temp, io.MultiReader(bytes.NewReader(header), reader))
	_ = input.Close()
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	staged, err := os.Open(temp.Name())
	if err != nil {
		return err
	}
	defer staged.Close()
	return atomicWriteReader(filePath, staged, existingMode(filePath))
}
