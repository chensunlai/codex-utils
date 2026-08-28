package history

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func syncRolloutFiles(paths Paths, settings ModelSettings, stats *Stats, dryRun bool) error {
	if !fileExists(paths.SessionsDir) {
		return nil
	}
	var files []string
	err := filepath.WalkDir(paths.SessionsDir, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "rollout-") && strings.HasSuffix(entry.Name(), ".jsonl") {
			files = append(files, filePath)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("scan rollout files: %w", err)
	}
	sort.Strings(files)
	for _, filePath := range files {
		stats.RolloutFilesSeen++
		changed, err := UpdateRolloutFile(filePath, settings, dryRun)
		if err != nil {
			return err
		}
		if changed {
			stats.RolloutFilesUpdated++
		}
	}
	return nil
}

func UpdateRolloutFile(filePath string, settings ModelSettings, dryRun bool) (bool, error) {
	raw, err := os.ReadFile(filePath)
	if err != nil {
		return false, fmt.Errorf("read rollout %s: %w", filePath, err)
	}
	if len(raw) == 0 {
		return false, nil
	}
	newline := bytes.IndexByte(raw, '\n')
	firstLine := raw
	remainder := []byte(nil)
	if newline >= 0 {
		firstLine = raw[:newline]
		remainder = raw[newline+1:]
	}
	firstLine = bytes.TrimSuffix(firstLine, []byte{'\r'})
	var record map[string]any
	if err := json.Unmarshal(firstLine, &record); err != nil {
		return false, nil
	}
	if recordType, _ := record["type"].(string); recordType != "session_meta" {
		return false, nil
	}
	target := record
	if payload, ok := record["payload"].(map[string]any); ok {
		target = payload
	}
	if !applyModelFields(target, settings, true) {
		return false, nil
	}
	if dryRun {
		return true, nil
	}
	encoded, err := marshalCompactJSON(record)
	if err != nil {
		return false, fmt.Errorf("encode rollout %s: %w", filePath, err)
	}
	content := make([]byte, 0, len(encoded)+1+len(remainder))
	content = append(content, encoded...)
	content = append(content, '\n')
	content = append(content, remainder...)
	if err := atomicWriteFile(filePath, content, existingMode(filePath)); err != nil {
		return false, fmt.Errorf("write rollout %s: %w", filePath, err)
	}
	return true, nil
}

func applyModelFields(record map[string]any, settings ModelSettings, addMissing bool) bool {
	changed := false
	providerKeys := []string{"model_provider", "modelProvider", "provider"}
	modelKeys := []string{"model", "model_name", "modelName"}
	providerFound := false
	for _, key := range providerKeys {
		if value, ok := record[key]; ok {
			providerFound = true
			if value != settings.Provider {
				record[key] = settings.Provider
				changed = true
			}
		}
	}
	modelFound := false
	for _, key := range modelKeys {
		if value, ok := record[key]; ok {
			modelFound = true
			if value != settings.Model {
				record[key] = settings.Model
				changed = true
			}
		}
	}
	if addMissing && !providerFound {
		record["model_provider"] = settings.Provider
		changed = true
	}
	if addMissing && !modelFound {
		record["model"] = settings.Model
		changed = true
	}
	return changed
}

func marshalCompactJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'}), nil
}
