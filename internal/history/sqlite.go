package history

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

func openStateDatabase(path string) (*sql.DB, error) {
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(1)
	if _, err := database.Exec("PRAGMA busy_timeout = 30000"); err != nil {
		database.Close()
		return nil, err
	}
	return database, nil
}

func tableColumns(database *sql.DB, table string) (map[string]bool, error) {
	rows, err := database.Query("SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

func syncStateDatabase(paths Paths, settings ModelSettings, stats *Stats, dryRun bool) error {
	if !fileExists(paths.StateDB) {
		return nil
	}
	database, err := openStateDatabase(paths.StateDB)
	if err != nil {
		return fmt.Errorf("open state database: %w", err)
	}
	defer database.Close()
	columns, err := tableColumns(database, "threads")
	if err != nil {
		return fmt.Errorf("inspect threads table: %w", err)
	}
	if !columns["id"] || !columns["model_provider"] || !columns["model"] {
		return nil
	}

	rows, err := database.Query("SELECT id, model_provider, model FROM threads")
	if err != nil {
		return fmt.Errorf("read threads: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		var provider, model sql.NullString
		if err := rows.Scan(&id, &provider, &model); err != nil {
			rows.Close()
			return fmt.Errorf("read thread row: %w", err)
		}
		stats.DBThreadsSeen++
		if !provider.Valid || !model.Valid || provider.String != settings.Provider || model.String != settings.Model {
			ids = append(ids, id)
		}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close thread rows: %w", err)
	}
	stats.DBThreadsUpdated = len(ids)
	if len(ids) == 0 || dryRun {
		return nil
	}

	transaction, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("start database transaction: %w", err)
	}
	statement, err := transaction.Prepare("UPDATE threads SET model_provider = ?, model = ? WHERE id = ?")
	if err != nil {
		transaction.Rollback()
		return fmt.Errorf("prepare thread update: %w", err)
	}
	for _, id := range ids {
		if _, err := statement.Exec(settings.Provider, settings.Model, id); err != nil {
			statement.Close()
			transaction.Rollback()
			return fmt.Errorf("update thread %s: %w", id, err)
		}
	}
	if err := statement.Close(); err != nil {
		transaction.Rollback()
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit thread updates: %w", err)
	}
	return nil
}

func readIndexEntriesFromDatabase(paths Paths, existing map[string]map[string]any) ([]map[string]any, bool, error) {
	if !fileExists(paths.StateDB) {
		return nil, false, nil
	}
	database, err := openStateDatabase(paths.StateDB)
	if err != nil {
		return nil, false, fmt.Errorf("open state database for index: %w", err)
	}
	defer database.Close()
	columns, err := tableColumns(database, "threads")
	if err != nil {
		return nil, false, fmt.Errorf("inspect threads table for index: %w", err)
	}
	if !columns["id"] {
		return nil, false, nil
	}

	selected := []string{"id"}
	for _, column := range []string{"title", "updated_at", "cwd", "git_branch", "git_sha", "git_origin_url", "rollout_path"} {
		if columns[column] {
			selected = append(selected, column)
		}
	}
	query := "SELECT " + strings.Join(selected, ", ") + " FROM threads"
	if columns["archived"] {
		query += " WHERE archived = 0"
	}
	query += " ORDER BY id ASC"
	rows, err := database.Query(query)
	if err != nil {
		return nil, false, fmt.Errorf("read index fields from database: %w", err)
	}
	defer rows.Close()

	entries := make([]map[string]any, 0)
	for rows.Next() {
		values := make([]any, len(selected))
		destinations := make([]any, len(selected))
		for index := range values {
			destinations[index] = &values[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			return nil, false, fmt.Errorf("scan index database row: %w", err)
		}
		row := make(map[string]any, len(selected))
		for index, column := range selected {
			row[column] = values[index]
		}
		threadID := databaseString(row["id"])
		entry := cloneMap(existing[threadID])
		entry["id"] = threadID
		title := databaseString(row["title"])
		if title == "" {
			title = threadID
		}
		if valueString(entry["thread_name"]) == "" {
			entry["thread_name"] = title
		}
		if timestamp, ok := unixTimestamp(row["updated_at"]); ok {
			entry["updated_at"] = timestamp.UTC().Format(time.RFC3339)
		} else if _, exists := entry["updated_at"]; !exists {
			entry["updated_at"] = ""
		}
		applyThreadMetadata(entry, row)
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("read index database rows: %w", err)
	}
	return entries, true, nil
}

func applyThreadMetadata(entry, row map[string]any) {
	for _, key := range []string{"cwd", "git_branch", "git_sha", "git_origin_url", "rollout_path"} {
		if value := databaseString(row[key]); value != "" {
			entry[key] = value
		}
	}
	metadata := map[string]string{
		"branch":         valueString(entry["git_branch"]),
		"commit_hash":    valueString(entry["git_sha"]),
		"repository_url": valueString(entry["git_origin_url"]),
	}
	hasMetadata := false
	git := make(map[string]any)
	if existing, ok := entry["git"].(map[string]any); ok {
		for key, value := range existing {
			git[key] = value
		}
	}
	for key, value := range metadata {
		if value != "" {
			git[key] = value
			hasMetadata = true
		}
	}
	if hasMetadata {
		entry["git"] = git
	}
}

func databaseString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return fmt.Sprint(typed)
	}
}

func valueString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func unixTimestamp(value any) (time.Time, bool) {
	var timestamp int64
	switch typed := value.(type) {
	case int64:
		timestamp = typed
	case int:
		timestamp = int64(typed)
	case float64:
		timestamp = int64(typed)
	case []byte:
		parsed, err := strconv.ParseInt(string(typed), 10, 64)
		if err != nil {
			return time.Time{}, false
		}
		timestamp = parsed
	case string:
		parsed, err := strconv.ParseInt(typed, 10, 64)
		if err != nil {
			return time.Time{}, false
		}
		timestamp = parsed
	default:
		return time.Time{}, false
	}
	if timestamp == 0 {
		return time.Time{}, false
	}
	if timestamp > 10_000_000_000 {
		timestamp /= 1000
	}
	return time.Unix(timestamp, 0), true
}

func cloneMap(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
