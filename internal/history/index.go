package history

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

func syncSessionIndex(paths Paths, settings ModelSettings, stats *Stats, dryRun bool) error {
	if !fileExists(paths.SessionIndex) && !fileExists(paths.StateDB) {
		return nil
	}
	var raw []byte
	var err error
	if fileExists(paths.SessionIndex) {
		raw, err = os.ReadFile(paths.SessionIndex)
		if err != nil {
			return fmt.Errorf("read session index: %w", err)
		}
	}
	lines := splitLines(raw)
	existing := make(map[string]map[string]any)
	order := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			stats.MalformedJSONLines++
			continue
		}
		threadID := strings.TrimSpace(valueString(record["id"]))
		if threadID == "" {
			continue
		}
		stats.IndexRowsSeen++
		existing[threadID] = record
		order = append(order, threadID)
	}

	databaseEntries, usedDatabase, err := readIndexEntriesFromDatabase(paths, existing)
	if err != nil {
		return err
	}
	var output []map[string]any
	if !usedDatabase {
		for _, threadID := range order {
			record := cloneMap(existing[threadID])
			applyModelFields(record, settings, false)
			output = append(output, record)
		}
	} else {
		output = append(output, databaseEntries...)
		databaseIDs := make(map[string]bool, len(databaseEntries))
		for _, entry := range databaseEntries {
			databaseIDs[valueString(entry["id"])] = true
		}
		for _, threadID := range order {
			if !databaseIDs[threadID] {
				output = append(output, cloneMap(existing[threadID]))
			}
		}
		for _, entry := range output {
			applyModelFields(entry, settings, false)
		}
		sort.SliceStable(output, func(left, right int) bool {
			leftTime := parseIndexTimestamp(valueString(output[left]["updated_at"]))
			rightTime := parseIndexTimestamp(valueString(output[right]["updated_at"]))
			if leftTime.Equal(rightTime) {
				return valueString(output[left]["id"]) < valueString(output[right]["id"])
			}
			return leftTime.Before(rightTime)
		})
	}

	var desired bytes.Buffer
	for _, entry := range output {
		encoded, err := marshalCompactJSON(entry)
		if err != nil {
			return fmt.Errorf("encode session index: %w", err)
		}
		desired.Write(encoded)
		desired.WriteByte('\n')
	}
	current := strings.Join(lines, "\n")
	if current != "" {
		current += "\n"
	}
	if desired.String() == current {
		return nil
	}
	stats.IndexRowsUpdated = len(output)
	if dryRun {
		return nil
	}
	if err := atomicWriteFile(paths.SessionIndex, desired.Bytes(), existingMode(paths.SessionIndex)); err != nil {
		return fmt.Errorf("write session index: %w", err)
	}
	return nil
}

func splitLines(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	parts := bytes.Split(raw, []byte{'\n'})
	if len(parts) > 0 && len(parts[len(parts)-1]) == 0 {
		parts = parts[:len(parts)-1]
	}
	lines := make([]string, len(parts))
	for index, part := range parts {
		lines[index] = string(bytes.TrimSuffix(part, []byte{'\r'}))
	}
	return lines
}

func parseIndexTimestamp(value string) time.Time {
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.UTC()
	}
	return time.Unix(0, 0).UTC()
}
