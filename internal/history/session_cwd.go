package history

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

// Relocate runtime paths while preserving messages and ordinals. References
// use the rewritten boundary of their named segment, including context records.
func relocateSessionCwd(stage string, manifest sessionManifest, cwd string) (map[string]bool, error) {
	catalog, err := readSessionCatalog(Paths{Home: stage, SessionsDir: filepath.Join(stage, "sessions"), SessionIndex: filepath.Join(stage, "session_index.jsonl"), StateDB: filepath.Join(stage, "state_5.sqlite")})
	if err != nil {
		return nil, err
	}
	type reference struct {
		file    *sessionFile
		ordinal int64
	}
	parents := make(map[string]reference)
	needed := make(map[string]map[int64]bool)
	for _, record := range catalog {
		for _, file := range record.Files {
			base := file.Header.Payload.HistoryBase
			if base == nil {
				continue
			}
			parent := historyDependencyRecord(catalog, base)
			if parent == nil {
				return nil, fmt.Errorf("missing history dependency for %s", file.Relative)
			}
			for _, candidate := range historyReferenceFiles(parent.Files, base.ThreadID) {
				if candidate.Header.Ordinal >= base.EndOrdinalExclusive {
					continue
				}
				if _, ok := ordinalByteBoundary(candidate.Path, base.EndOrdinalExclusive); ok {
					parents[file.Path] = reference{candidate, base.EndOrdinalExclusive}
					if needed[candidate.Path] == nil {
						needed[candidate.Path] = make(map[int64]bool)
					}
					needed[candidate.Path][base.EndOrdinalExclusive] = true
					break
				}
			}
			if parents[file.Path].file == nil {
				return nil, fmt.Errorf("missing history boundary for %s", file.Relative)
			}
		}
	}
	boundaries := make(map[string]map[int64]int64)
	visiting := make(map[string]bool)
	reindex := make(map[string]bool)
	var relocate func(*sessionFile) error
	relocate = func(file *sessionFile) error {
		if _, done := boundaries[file.Path]; done {
			return nil
		}
		if visiting[file.Path] {
			return fmt.Errorf("cyclic history dependency: %s", file.Relative)
		}
		visiting[file.Path] = true
		parent := parents[file.Path]
		var cutoff *int64
		if parent.file != nil {
			if err := relocate(parent.file); err != nil {
				return err
			}
			offset := boundaries[parent.file.Path][parent.ordinal]
			cutoff = &offset
		}
		offsets, changed, err := rewriteSessionLocations(file.Path, cwd, cutoff, needed[file.Path])
		if err != nil {
			return err
		}
		boundaries[file.Path] = offsets
		if changed {
			reindex[file.Header.Payload.ID] = true
		}
		return nil
	}
	for _, session := range manifest.Sessions {
		for _, file := range catalog[session.ID].Files {
			if err := relocate(file); err != nil {
				return nil, err
			}
		}
	}
	return reindex, nil
}

func rewriteSessionLocations(filePath, cwd string, cutoff *int64, needed map[int64]bool) (map[int64]int64, bool, error) {
	input, err := os.Open(filePath)
	if err != nil {
		return nil, false, err
	}
	defer input.Close()
	output, err := os.CreateTemp(filepath.Dir(filePath), ".workspace-*")
	if err != nil {
		return nil, false, err
	}
	defer os.Remove(output.Name())
	defer output.Close()
	if err := output.Chmod(existingMode(filePath).Perm()); err != nil {
		return nil, false, err
	}
	reader := bufio.NewReader(input)
	offsets := make(map[int64]int64)
	var written int64
	changed, reindex := false, false
	for {
		line, readErr := reader.ReadBytes('\n')
		if readErr != nil && readErr != io.EOF {
			return nil, false, readErr
		}
		if len(line) == 0 {
			break
		}
		var record map[string]any
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.UseNumber()
		valid := decoder.Decode(&record) == nil
		adapted := line
		if valid {
			modified := relocateRuntimePaths(record, cwd)
			if record["type"] == "session_meta" && cutoff != nil {
				payload, _ := record["payload"].(map[string]any)
				base, _ := payload["history_base"].(map[string]any)
				value, _ := base["end_byte_offset"].(json.Number)
				if base != nil && value.String() != strconv.FormatInt(*cutoff, 10) {
					base["end_byte_offset"] = *cutoff
					modified, reindex = true, true
				}
			}
			if modified {
				encoded, err := marshalCompactJSON(record)
				if err != nil {
					return nil, false, err
				}
				ending := []byte(nil)
				if bytes.HasSuffix(line, []byte("\r\n")) {
					ending = []byte("\r\n")
				} else if bytes.HasSuffix(line, []byte("\n")) {
					ending = []byte("\n")
				}
				if padding := len(line) - len(encoded) - len(ending); padding > 0 {
					encoded = append(encoded, bytes.Repeat([]byte{' '}, padding)...)
				}
				adapted = append(encoded, ending...)
				changed = true
				if len(adapted) != len(line) {
					reindex = true
				}
			}
		}
		n, err := output.Write(adapted)
		if err != nil {
			return nil, false, err
		}
		written += int64(n)
		if ordinal, ok := record["ordinal"].(json.Number); valid && ok {
			number, err := ordinal.Int64()
			if err == nil && needed[number+1] {
				offsets[number+1] = written
			}
		}
		if readErr == io.EOF {
			break
		}
	}
	for ordinal := range needed {
		if _, ok := offsets[ordinal]; !ok {
			return nil, false, fmt.Errorf("missing history boundary %d in %s", ordinal, filePath)
		}
	}
	if !changed {
		return offsets, false, nil
	}
	if err := output.Sync(); err != nil {
		return nil, false, err
	}
	if err := output.Close(); err != nil {
		return nil, false, err
	}
	if err := input.Close(); err != nil {
		return nil, false, err
	}
	if err := replaceFile(output.Name(), filePath); err != nil {
		return nil, false, err
	}
	return offsets, reindex, nil
}

func relocateRuntimePaths(record map[string]any, cwd string) bool {
	payload, _ := record["payload"].(map[string]any)
	if payload == nil {
		return false
	}
	rootsKey := "runtime_workspace_roots"
	switch record["type"] {
	case "session_meta":
	case "turn_context":
		rootsKey = "workspace_roots"
	case "event_msg":
		if payload["type"] != "thread_settings_applied" {
			return false
		}
		payload, _ = payload["thread_settings"].(map[string]any)
		if payload == nil {
			return false
		}
	default:
		return false
	}
	changed := payload["cwd"] != cwd
	payload["cwd"] = cwd
	for _, key := range []string{"runtime_workspace_roots", "workspace_roots"} {
		if _, exists := payload[key]; !exists && key != rootsKey {
			continue
		}
		roots, ok := payload[key].([]any)
		if !ok || len(roots) != 1 || roots[0] != cwd {
			payload[key] = []string{cwd}
			changed = true
		}
	}
	return changed
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
