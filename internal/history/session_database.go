package history

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const historyDBName = "thread_history_1.sqlite"

type transferTable struct {
	Name string
	Key  string
}

var stateTransferTables = []transferTable{
	{"threads", "id"},
	{"thread_dynamic_tools", "thread_id"},
	{"thread_attachments", "thread_id"},
	{"thread_spawn_edges", "child_thread_id"},
}

var historyTransferTables = []transferTable{
	{"thread_turns", "thread_id"},
	{"thread_items", "thread_id"},
	{"thread_realtime_items", "thread_id"},
	{"thread_history_projection_state", "thread_id"},
}

func transferTables(name string) []transferTable {
	if name == StateDBName {
		return stateTransferTables
	}
	return historyTransferTables
}

func openReadOnlyDatabase(filePath string) (*sql.DB, error) {
	uriPath := filepath.ToSlash(filePath)
	if filepath.VolumeName(filePath) != "" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	uri := url.URL{Scheme: "file", Path: uriPath, RawQuery: "mode=ro"}
	database, err := sql.Open("sqlite", uri.String())
	if err == nil {
		database.SetMaxOpenConns(1)
	}
	return database, err
}

type rowQueryer interface {
	Query(string, ...any) (*sql.Rows, error)
}

func queryRecords(database rowQueryer, query string, args ...any) ([]map[string]any, error) {
	rows, err := database.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var records []map[string]any
	for rows.Next() {
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for i := range values {
			destinations[i] = &values[i]
		}
		if err := rows.Scan(destinations...); err != nil {
			return nil, err
		}
		record := make(map[string]any, len(columns))
		for i, column := range columns {
			record[column] = values[i]
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func readThreadRecords(filePath string) (map[string]map[string]any, error) {
	threads := make(map[string]map[string]any)
	if !fileExists(filePath) {
		return threads, nil
	}
	database, err := openReadOnlyDatabase(filePath)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	columns, err := tableColumns(database, "threads")
	if err != nil || !columns["id"] {
		return threads, err
	}
	rows, err := queryRecords(database, "SELECT * FROM threads")
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		threads[databaseString(row["id"])] = row
	}
	return threads, nil
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

// The snapshot contains the original schema and migrations, but only the
// requested conversations. Other tables are empty so a fresh Codex home can
// initialize the database without losing its migration history.
func createTransferDatabase(source, target, name string, ids []string) (retErr error) {
	database, err := openReadOnlyDatabase(source)
	if err != nil {
		return err
	}
	defer database.Close()
	read, err := database.Begin()
	if err != nil {
		return err
	}
	defer read.Rollback()
	schema, err := queryRecords(read, "SELECT type, name, sql FROM sqlite_master WHERE sql IS NOT NULL AND type IN ('table', 'index') AND name NOT LIKE 'sqlite_%' ORDER BY CASE type WHEN 'table' THEN 0 ELSE 1 END, name")
	if err != nil {
		return err
	}
	snapshot, err := sql.Open("sqlite", target)
	if err != nil {
		return err
	}
	defer snapshot.Close()
	write, err := snapshot.Begin()
	if err != nil {
		return err
	}
	defer write.Rollback()
	tables := make(map[string]bool)
	for _, item := range schema {
		if _, err := write.Exec(databaseString(item["sql"])); err != nil {
			return fmt.Errorf("copy %s schema: %w", name, err)
		}
		if item["type"] == "table" {
			tables[databaseString(item["name"])] = true
		}
	}
	selected := make(map[string]bool, len(ids))
	for _, id := range ids {
		selected[id] = true
	}
	copyTables := append([]transferTable{{"_sqlx_migrations", ""}}, transferTables(name)...)
	for _, table := range copyTables {
		if !tables[table.Name] {
			continue
		}
		query := "SELECT * FROM " + quoteIdentifier(table.Name)
		var args []any
		if table.Key != "" {
			query += " WHERE " + quoteIdentifier(table.Key) + " IN (" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")"
			for _, id := range ids {
				args = append(args, id)
			}
		}
		rows, err := queryRecords(read, query, args...)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if table.Name == "thread_spawn_edges" && !selected[databaseString(row["parent_thread_id"])] {
				continue
			}
			if table.Name == "threads" {
				for _, field := range []string{"project_id", "thread_section_id"} {
					if _, exists := row[field]; exists {
						row[field] = nil
					}
				}
			}
			if err := insertDatabaseRecord(write, quoteIdentifier(table.Name), row, nil); err != nil {
				return err
			}
		}
	}
	return write.Commit()
}

func insertDatabaseRecord(transaction *sql.Tx, table string, row map[string]any, columns map[string]bool) error {
	keys := make([]string, 0, len(row))
	for key := range row {
		if columns == nil || columns[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var names []string
	var values []any
	for _, key := range keys {
		names = append(names, quoteIdentifier(key))
		values = append(values, row[key])
	}
	query := "INSERT INTO " + table + " (" + strings.Join(names, ",") + ") VALUES (" + strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",") + ") ON CONFLICT DO NOTHING"
	_, err := transaction.Exec(query, values...)
	return err
}

func validateTransferDatabase(filePath, name string, ids map[string]bool) error {
	database, err := openReadOnlyDatabase(filePath)
	if err != nil {
		return err
	}
	defer database.Close()
	var integrity string
	if err := database.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		return fmt.Errorf("invalid %s database: %s (%v)", name, integrity, err)
	}
	var unsafe int
	if err := database.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type IN ('view', 'trigger')").Scan(&unsafe); err != nil {
		return err
	}
	if unsafe != 0 {
		return fmt.Errorf("%s contains unsupported views or triggers", name)
	}
	allowed := map[string]string{"_sqlx_migrations": ""}
	for _, table := range transferTables(name) {
		allowed[table.Name] = table.Key
	}
	tables, err := queryRecords(database, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		return err
	}
	for _, table := range tables {
		tableName := databaseString(table["name"])
		key, known := allowed[tableName]
		if !known {
			var count int
			if err := database.QueryRow("SELECT COUNT(*) FROM " + quoteIdentifier(tableName)).Scan(&count); err != nil || count != 0 {
				return fmt.Errorf("%s contains unrelated data in %s", name, tableName)
			}
			continue
		}
		if key == "" {
			continue
		}
		rows, err := queryRecords(database, "SELECT "+quoteIdentifier(key)+" FROM "+quoteIdentifier(tableName))
		if err != nil {
			return err
		}
		for _, row := range rows {
			if !ids[databaseString(row[key])] {
				return fmt.Errorf("%s contains an undeclared conversation", name)
			}
		}
	}
	return nil
}

func bootstrapTransferDatabase(source, target, name string) (retErr error) {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := writeNewFile(target, input); err != nil {
		return err
	}
	defer func() {
		if retErr != nil {
			_ = os.Remove(target)
			_ = os.Remove(target + "-wal")
			_ = os.Remove(target + "-shm")
			_ = os.Remove(target + "-journal")
		}
	}()
	database, err := sql.Open("sqlite", target)
	if err != nil {
		return err
	}
	defer database.Close()
	for _, table := range transferTables(name) {
		columns, err := tableColumns(database, table.Name)
		if err != nil {
			return err
		}
		if len(columns) > 0 {
			if _, err := database.Exec("DELETE FROM " + quoteIdentifier(table.Name)); err != nil {
				return err
			}
		}
	}
	return nil
}

func destinationColumns(transaction *sql.Tx, schema, table string) (map[string]bool, error) {
	rows, err := queryRecords(transaction, "PRAGMA "+schema+".table_info("+quoteIdentifier(table)+")")
	if err != nil {
		return nil, err
	}
	columns := make(map[string]bool, len(rows))
	for _, row := range rows {
		columns[databaseString(row["name"])] = true
	}
	return columns, nil
}

func mergeTransferDatabase(transaction *sql.Tx, schema, source, name string, sessions map[string]archiveSession, primary map[string]string, cwd, provider string, reindex map[string]bool) error {
	var database *sql.DB
	var err error
	if source != "" {
		database, err = openReadOnlyDatabase(source)
		if err != nil {
			return err
		}
		defer database.Close()
	}
	for _, table := range transferTables(name) {
		columns, err := destinationColumns(transaction, schema, table.Name)
		if err != nil {
			return err
		}
		if len(columns) == 0 {
			if table.Name == "threads" {
				return fmt.Errorf("destination database has no threads table")
			}
			continue
		}
		if name == historyDBName {
			for id := range reindex {
				if _, err := transaction.Exec("DELETE FROM "+schema+"."+quoteIdentifier(table.Name)+" WHERE thread_id = ?", id); err != nil {
					return err
				}
			}
		}
		var rows []map[string]any
		if database != nil {
			sourceColumns, err := tableColumns(database, table.Name)
			if err != nil {
				return err
			}
			if len(sourceColumns) > 0 {
				rows, err = queryRecords(database, "SELECT * FROM "+quoteIdentifier(table.Name))
				if err != nil {
					return err
				}
			}
		}
		seen := make(map[string]bool)
		for _, row := range rows {
			id := databaseString(row[table.Key])
			session, selected := sessions[id]
			if !selected || (name == historyDBName && reindex[id]) {
				continue
			}
			if table.Name == "thread_history_projection_state" {
				turnColumns, err := tableColumns(database, "thread_turns")
				if err != nil {
					return err
				}
				var turns int
				if len(turnColumns) > 0 {
					if err := database.QueryRow("SELECT COUNT(*) FROM thread_turns WHERE thread_id = ?", id).Scan(&turns); err != nil {
						return err
					}
				}
				if turns == 0 {
					// v0.2.0 archives omitted turns but retained the projection
					// cursor. Let Codex rebuild the index from intact rollouts.
					if _, err := transaction.Exec("DELETE FROM "+schema+".thread_history_projection_state WHERE thread_id = ?", id); err != nil {
						return err
					}
					continue
				}
			}
			if table.Name == "threads" {
				var exists int
				if err := transaction.QueryRow("SELECT COUNT(*) FROM "+schema+".threads WHERE id = ?", id).Scan(&exists); err != nil {
					return err
				}
				seen[id] = true
				if exists != 0 {
					if columns["model_provider"] {
						if _, err := transaction.Exec("UPDATE "+schema+".threads SET model_provider = ? WHERE id = ?", provider, id); err != nil {
							return err
						}
					}
					if cwd != "" && columns["cwd"] {
						if _, err := transaction.Exec("UPDATE "+schema+".threads SET cwd = ? WHERE id = ?", cwd, id); err != nil {
							return err
						}
					}
					continue
				}
				prepareImportedThread(row, session, primary[id], cwd)
				row["model_provider"] = provider
			}
			if err := insertDatabaseRecord(transaction, schema+"."+quoteIdentifier(table.Name), row, columns); err != nil {
				return fmt.Errorf("import %s/%s: %w", name, table.Name, err)
			}
		}
		if table.Name == "threads" {
			for id, session := range sessions {
				if seen[id] {
					continue
				}
				row := fallbackThread(session)
				prepareImportedThread(row, session, primary[id], cwd)
				row["model_provider"] = provider
				if err := insertDatabaseRecord(transaction, schema+".threads", row, columns); err != nil {
					return fmt.Errorf("register conversation %s: %w", id, err)
				}
			}
		}
	}
	return nil
}

func prepareImportedThread(row map[string]any, session archiveSession, rolloutPath, cwd string) {
	row["rollout_path"] = rolloutPath
	row["archived"] = int64(0)
	row["archived_at"] = nil
	row["thread_section_id"] = nil
	row["project_id"] = nil
	if cwd != "" {
		row["cwd"] = cwd
	} else if row["cwd"] == nil {
		row["cwd"] = session.Cwd
	}
}

func fallbackThread(session archiveSession) map[string]any {
	updated, err := time.Parse(time.RFC3339Nano, session.UpdatedAt)
	if err != nil {
		updated = time.Now()
	}
	created := updated
	if timestamp, err := time.Parse(time.RFC3339Nano, session.Meta.Payload.Timestamp); err == nil {
		created = timestamp
	}
	provider := session.Meta.Payload.Provider
	if provider == "" {
		provider = DefaultProvider
	}
	source := "cli"
	if raw := session.Meta.Payload.Source; len(raw) > 0 {
		source = strings.Trim(string(raw), `"`)
	}
	mode := session.HistoryMode
	if mode == "" {
		mode = "legacy"
	}
	return map[string]any{
		"id": session.ID, "created_at": created.Unix(), "updated_at": updated.Unix(),
		"created_at_ms": created.UnixMilli(), "updated_at_ms": updated.UnixMilli(),
		"source": source, "model_provider": provider, "model": session.Meta.Payload.Model,
		"cwd": session.Cwd, "title": session.Title, "sandbox_policy": `"read-only"`,
		"approval_mode": "on-request", "cli_version": session.Meta.Payload.CLIVersion,
		"history_mode": mode, "has_user_event": int64(1), "tokens_used": int64(0),
	}
}
