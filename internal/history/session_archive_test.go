package history

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func transferTestDatabase(t *testing.T, paths Paths) *sql.DB {
	t.Helper()
	database := openTestDatabase(t, paths.StateDB)
	mustExec(t, database, `CREATE TABLE threads (
		id TEXT PRIMARY KEY, rollout_path TEXT NOT NULL, created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL, source TEXT NOT NULL, model_provider TEXT NOT NULL,
		cwd TEXT NOT NULL, title TEXT NOT NULL, sandbox_policy TEXT NOT NULL,
		approval_mode TEXT NOT NULL, archived INTEGER NOT NULL DEFAULT 0,
		archived_at INTEGER, model TEXT, history_mode TEXT NOT NULL DEFAULT 'legacy',
		project_id TEXT, thread_section_id TEXT)`)
	mustExec(t, database, `CREATE TABLE thread_dynamic_tools (thread_id TEXT, position INTEGER, name TEXT, description TEXT, input_schema TEXT, PRIMARY KEY(thread_id, position))`)
	mustExec(t, database, `CREATE TABLE projects (id TEXT PRIMARY KEY, title TEXT)`)
	mustExec(t, database, `CREATE TABLE _sqlx_migrations (version INTEGER PRIMARY KEY, description TEXT, installed_on TIMESTAMP DEFAULT CURRENT_TIMESTAMP, success BOOLEAN, checksum BLOB, execution_time INTEGER)`)
	mustExec(t, database, `INSERT INTO _sqlx_migrations(version, description, success, checksum, execution_time) VALUES (1, 'initial', 1, ?, 10)`, []byte{0, 1, 128, 255})
	return database
}

func transferTestHistoryDatabase(t *testing.T, paths Paths) *sql.DB {
	t.Helper()
	database := openTestDatabase(t, filepath.Join(paths.Home, historyDBName))
	mustExec(t, database, `CREATE TABLE thread_items (thread_id TEXT, turn_id TEXT, item_id TEXT, rollout_ordinal INTEGER, item_json TEXT, PRIMARY KEY(thread_id, turn_id, item_id))`)
	mustExec(t, database, `CREATE TABLE thread_turns (thread_id TEXT, turn_id TEXT, rollout_ordinal INTEGER, status TEXT, rollout_byte_offset INTEGER, rollout_end_byte_offset INTEGER, PRIMARY KEY(thread_id, turn_id))`)
	mustExec(t, database, `CREATE TABLE thread_history_projection_state (thread_id TEXT PRIMARY KEY, next_rollout_byte_offset INTEGER, next_rollout_ordinal INTEGER)`)
	return database
}

func transferTestRollout(t *testing.T, paths Paths, id, filename string, ordinal int64, base *historyBase, eventOrdinals ...int64) string {
	t.Helper()
	filePath := filepath.Join(paths.SessionsDir, "2026", "10", "05", filename)
	payload := map[string]any{
		"id": id, "timestamp": "2026-10-05T08:00:00Z", "cwd": `C:\Users\source\project`,
		"model_provider": "source-provider", "model": "source-model", "source": "cli",
		"history_mode": "paginated", "cli_version": "0.160.0",
		"runtime_workspace_roots": []string{`C:\Users\source\project`},
	}
	if base != nil {
		payload["history_base"] = base
		payload["forked_from_id"] = base.ThreadID
	}
	records := []map[string]any{{"type": "session_meta", "ordinal": ordinal, "payload": payload}}
	for _, n := range eventOrdinals {
		records = append(records, map[string]any{"type": "response_item", "ordinal": n, "payload": map[string]any{"role": "user", "text": "message to preserve"}})
	}
	writeJSONLines(t, filePath, records...)
	return filePath
}

func insertTransferThread(t *testing.T, database *sql.DB, id, rollout string) {
	t.Helper()
	mustExec(t, database, `INSERT INTO threads(id, rollout_path, created_at, updated_at, source, model_provider, cwd, title, sandbox_policy, approval_mode, model, history_mode, project_id, thread_section_id) VALUES (?, ?, 1760000000, 1760000100, 'cli', 'source-provider', ?, ?, '"read-only"', 'on-request', 'source-model', 'paginated', 'source-project', 'source-section')`, id, rollout, `C:\Users\source\project`, "Title "+id)
}

func TestSessionArchiveAddsMultipleConversationsWithoutReplacingLocalHistory(t *testing.T) {
	source := testPaths(t)
	database := transferTestDatabase(t, source)
	// Include uncheckpointed metadata in the transfer snapshot.
	mustExec(t, database, "PRAGMA journal_mode=WAL")
	for _, id := range []string{"one", "two", "unselected"} {
		rollout := transferTestRollout(t, source, id, "rollout-"+id+".jsonl", 0, nil, 1, 2)
		insertTransferThread(t, database, id, rollout)
	}
	mustExec(t, database, `INSERT INTO projects VALUES ('source-project', 'private unrelated project')`)
	mustExec(t, database, `INSERT INTO thread_dynamic_tools VALUES ('one', 0, 'tool', 'description', '{}')`)
	defer database.Close()
	writeJSONLines(t, source.SessionIndex, map[string]any{"id": "one", "thread_name": "Renamed One", "custom": true, "git": map[string]any{"branch": "main"}})
	writeFile(t, source.Config, "secret-config", 0o600)
	writeFile(t, filepath.Join(source.Home, "auth.json"), "secret-auth", 0o600)
	archive := filepath.Join(t.TempDir(), "conversations.zip")
	stats, err := ExportSessions(source, []string{"one", "two", "one"}, archive)
	if err != nil || stats.Selected != 2 || stats.Dependencies != 0 || stats.Files != 2 {
		t.Fatalf("ExportSessions = %#v, %v", stats, err)
	}
	reader, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range reader.File {
		if strings.Contains(file.Name, "auth") || strings.Contains(file.Name, "config") || strings.Contains(file.Name, "unselected") || strings.Contains(file.Name, `\`) {
			t.Fatalf("unexpected ZIP member: %s", file.Name)
		}
	}
	reader.Close()
	destination := testPaths(t)
	localDatabase := transferTestDatabase(t, destination)
	local := transferTestRollout(t, destination, "local", "rollout-local.jsonl", 0, nil, 1)
	insertTransferThread(t, localDatabase, "local", local)
	localDatabase.Close()
	indexBefore := []byte("{\"id\":\"local\",\"custom\":\"keep\"}\nmalformed but preserved\n")
	writeFile(t, destination.SessionIndex, string(indexBefore), 0o640)
	localBefore := readFile(t, local)
	cwd := filepath.Join(t.TempDir(), "本机项目 with spaces")
	imported, err := ImportSessions(destination, archive, cwd)
	if err != nil || imported.Added != 2 || imported.Skipped != 0 {
		t.Fatalf("ImportSessions = %#v, %v", imported, err)
	}
	if !bytes.Equal(readFile(t, local), localBefore) || !bytes.HasPrefix(readFile(t, destination.SessionIndex), indexBefore) {
		t.Fatal("existing conversation or index contents changed")
	}
	threads, err := readThreadRecords(destination.StateDB)
	if err != nil || len(threads) != 3 {
		t.Fatalf("threads = %v, %v", threads, err)
	}
	for _, id := range []string{"one", "two"} {
		row := threads[id]
		if row["cwd"] != cwd || row["project_id"] != nil || row["thread_section_id"] != nil {
			t.Fatalf("imported metadata = %#v", row)
		}
		rollout := databaseString(row["rollout_path"])
		if !strings.HasPrefix(rollout, destination.Home) || !sameSessionHistory(rollout, filepath.Join(source.SessionsDir, "2026", "10", "05", "rollout-"+id+".jsonl")) || row["model_provider"] != DefaultProvider {
			t.Fatalf("rollout path or content changed: %s", rollout)
		}
	}
	index, _, _ := readSessionIndex(destination.SessionIndex)
	if index["one"]["custom"] != true || index["one"]["thread_name"] != "Renamed One" || index["one"]["cwd"] != cwd {
		t.Fatalf("imported index = %#v", index["one"])
	}
	second, err := ImportSessions(destination, archive, cwd)
	if err != nil || second.Added != 0 || second.Skipped != 2 {
		t.Fatalf("repeat import = %#v, %v", second, err)
	}
}

func TestSessionArchivePreservesForkChainsAndRolloutSegments(t *testing.T) {
	source := testPaths(t)
	state := transferTestDatabase(t, source)
	history := transferTestHistoryDatabase(t, source)
	root := transferTestRollout(t, source, "root", "rollout-root.jsonl", 0, nil, 1, 2)
	rootSize := int64(len(readFile(t, root)))
	branch := transferTestRollout(t, source, "branch", "rollout-branch.jsonl", 3, &historyBase{"root", rootSize, 3}, 4)
	branchSize := int64(len(readFile(t, branch)))
	segmentID := "11111111-2222-3333-4444-555555555555"
	segment := transferTestRollout(t, source, "branch", "rollout-branch_"+segmentID+".jsonl", 5, &historyBase{"branch", branchSize, 5}, 6)
	segmentSize := int64(len(readFile(t, segment)))
	leaf := transferTestRollout(t, source, "leaf", "rollout-leaf.jsonl", 7, &historyBase{segmentID, segmentSize, 7}, 8)
	for id, rollout := range map[string]string{"root": root, "branch": segment, "leaf": leaf} {
		insertTransferThread(t, state, id, rollout)
		mustExec(t, history, `INSERT INTO thread_items VALUES (?, 'turn', 'item', 8, '{"content":"keep"}')`, id)
		mustExec(t, history, `INSERT INTO thread_turns VALUES (?, 'turn', 7, 'completed', 100, 200)`, id)
		mustExec(t, history, `INSERT INTO thread_history_projection_state VALUES (?, ?, 9)`, id, len(readFile(t, rollout)))
	}
	state.Close()
	history.Close()
	archive := filepath.Join(t.TempDir(), "fork.zip")
	stats, err := ExportSessions(source, []string{"leaf"}, archive)
	if err != nil || stats.Dependencies != 2 || stats.Files != 4 {
		t.Fatalf("fork export = %#v, %v", stats, err)
	}
	destination, err := ResolvePaths(filepath.Join(t.TempDir(), "new-machine", ".codex"))
	if err != nil {
		t.Fatal(err)
	}
	imported, err := ImportSessions(destination, archive, "")
	if err != nil || imported.Added != 3 || imported.Dependencies != 2 {
		t.Fatalf("fork import = %#v, %v", imported, err)
	}
	for _, rollout := range []string{root, branch, segment, leaf} {
		relative, _ := filepath.Rel(source.Home, rollout)
		importedPath := filepath.Join(destination.Home, relative)
		if !sameSessionHistory(rollout, importedPath) || len(readFile(t, rollout)) != len(readFile(t, importedPath)) {
			t.Fatalf("fork byte offsets invalidated: %s", rollout)
		}
	}
	db := openTestDatabase(t, destination.StateDB)
	defer db.Close()
	var checksum []byte
	if err := db.QueryRow("SELECT checksum FROM _sqlx_migrations WHERE version=1").Scan(&checksum); err != nil || !bytes.Equal(checksum, []byte{0, 1, 128, 255}) {
		t.Fatalf("migration checksum = %v, %v", checksum, err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM projects").Scan(&count); err != nil || count != 0 {
		t.Fatalf("unrelated project data copied: %d, %v", count, err)
	}
	history = openTestDatabase(t, filepath.Join(destination.Home, historyDBName))
	defer history.Close()
	if err := history.QueryRow("SELECT COUNT(*) FROM thread_items").Scan(&count); err != nil || count != 3 {
		t.Fatalf("history item count = %d, %v", count, err)
	}
	if err := history.QueryRow("SELECT COUNT(*) FROM thread_turns WHERE rollout_byte_offset=100 AND rollout_end_byte_offset=200").Scan(&count); err != nil || count != 3 {
		t.Fatalf("history turn count = %d, %v", count, err)
	}
	var offset int64
	if err := history.QueryRow("SELECT next_rollout_byte_offset FROM thread_history_projection_state WHERE thread_id='branch'").Scan(&offset); err != nil || offset != segmentSize {
		t.Fatalf("projection offset = %d, %v", offset, err)
	}
	threads, _ := readThreadRecords(destination.StateDB)
	if filepath.Base(databaseString(threads["branch"]["rollout_path"])) != filepath.Base(segment) {
		t.Fatal("did not preserve primary rollout segment")
	}
}

func TestSessionArchiveConflictRejectsEntireImport(t *testing.T) {
	source := testPaths(t)
	transferTestRollout(t, source, "conflict", "rollout-conflict.jsonl", 0, nil, 1)
	transferTestRollout(t, source, "new", "rollout-new.jsonl", 0, nil, 1)
	archive := filepath.Join(t.TempDir(), "conflict.zip")
	if _, err := ExportSessions(source, []string{"conflict", "new"}, archive); err != nil {
		t.Fatal(err)
	}
	destination := testPaths(t)
	local := transferTestRollout(t, destination, "conflict", "rollout-conflict.jsonl", 0, nil, 1, 2)
	before := readFile(t, local)
	writeFile(t, destination.SessionIndex, "existing index\n", 0o600)
	if _, err := ImportSessions(destination, archive, ""); err == nil || !strings.Contains(err.Error(), "different history") {
		t.Fatalf("conflict import error = %v", err)
	}
	if !bytes.Equal(readFile(t, local), before) || string(readFile(t, destination.SessionIndex)) != "existing index\n" {
		t.Fatal("conflict modified existing data")
	}
	if fileExists(filepath.Join(destination.SessionsDir, "2026", "10", "05", "rollout-new.jsonl")) {
		t.Fatal("conflict partially imported new conversation")
	}
}

func TestSessionArchiveRejectsMissingForkHistoryAndExistingOutput(t *testing.T) {
	paths := testPaths(t)
	transferTestRollout(t, paths, "fork", "rollout-fork.jsonl", 2, &historyBase{"missing", 100, 2}, 3)
	archive := filepath.Join(t.TempDir(), "missing.zip")
	if _, err := ExportSessions(paths, []string{"fork"}, archive); err == nil || fileExists(archive) {
		t.Fatalf("missing dependency export = %v", err)
	}
	transferTestRollout(t, paths, "normal", "rollout-normal.jsonl", 0, nil, 1)
	writeFile(t, archive, "keep existing output", 0o600)
	if _, err := ExportSessions(paths, []string{"normal"}, archive); err == nil || string(readFile(t, archive)) != "keep existing output" {
		t.Fatal("export replaced existing output")
	}
}

func TestSessionArchiveValidatesAllZipMembersBeforeImport(t *testing.T) {
	paths := testPaths(t)
	transferTestRollout(t, paths, "one", "rollout-one.jsonl", 0, nil, 1)
	archive := filepath.Join(t.TempDir(), "safe.zip")
	if _, err := ExportSessions(paths, []string{"one"}, archive); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../outside.jsonl", "sessions/../../outside.jsonl", `sessions\rollout-one.jsonl`, "C:/rollout-one.jsonl", "/rollout-one.jsonl", "sessions/rollout-one.jsonl:stream", "sessions/CON/rollout-one.jsonl", "sessions/LPT1.txt/rollout-one.jsonl", "unrelated.txt"} {
		t.Run(name, func(t *testing.T) {
			bad := rewriteTransferZip(t, archive, func(writer *zip.Writer, _ string, _ []byte) {}, name, false)
			destination := testPaths(t)
			if _, err := ImportSessions(destination, bad, ""); err == nil {
				t.Fatal("unsafe ZIP member accepted")
			}
			entries, _ := os.ReadDir(destination.Home)
			if len(entries) != 0 {
				t.Fatal("validation failure wrote destination files")
			}
		})
	}
	for _, mode := range []string{"checksum", "symlink", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			bad := rewriteTransferZip(t, archive, func(writer *zip.Writer, name string, raw []byte) {
				if !strings.HasSuffix(name, ".jsonl") {
					return
				}
				if mode == "checksum" {
					raw = bytes.Replace(raw, []byte("source-model"), []byte("changed-model"), 1)
				}
				header := &zip.FileHeader{Name: name, Method: zip.Deflate}
				header.SetMode(0o600)
				if mode == "symlink" {
					header.SetMode(os.ModeSymlink | 0o777)
				}
				entry, err := writer.CreateHeader(header)
				if err != nil {
					t.Fatal(err)
				}
				entry.Write(raw)
				if mode == "duplicate" {
					entry, _ := writer.Create(name)
					entry.Write(raw)
				}
			}, "", true)
			destination := testPaths(t)
			if _, err := ImportSessions(destination, bad, ""); err == nil {
				t.Fatal("invalid ZIP accepted")
			}
			entries, _ := os.ReadDir(destination.Home)
			if len(entries) != 0 {
				t.Fatal("invalid ZIP wrote destination files")
			}
		})
	}
}

func rewriteTransferZip(t *testing.T, source string, replace func(*zip.Writer, string, []byte), extra string, replacing bool) string {
	t.Helper()
	reader, err := zip.OpenReader(source)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	outputPath := filepath.Join(t.TempDir(), "modified.zip")
	output, err := os.Create(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(output)
	for _, file := range reader.File {
		input, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(input)
		input.Close()
		if err != nil {
			t.Fatal(err)
		}
		if replacing && strings.HasSuffix(file.Name, ".jsonl") {
			replace(writer, file.Name, raw)
			continue
		}
		entry, _ := writer.Create(file.Name)
		entry.Write(raw)
	}
	if extra != "" {
		entry, _ := writer.Create(extra)
		entry.Write([]byte("unsafe"))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	return outputPath
}

func TestSessionArchiveImportsArchivedRolloutsAsActiveConversations(t *testing.T) {
	source := testPaths(t)
	rollout := transferTestRollout(t, source, "archived", "rollout-archived.jsonl", 0, nil, 1)
	archived := filepath.Join(source.Home, "archived_sessions", filepath.Base(rollout))
	os.MkdirAll(filepath.Dir(archived), 0o755)
	if err := os.Rename(rollout, archived); err != nil {
		t.Fatal(err)
	}
	database := transferTestDatabase(t, source)
	insertTransferThread(t, database, "archived", archived)
	mustExec(t, database, "UPDATE threads SET archived=1, archived_at=1760000001")
	database.Close()
	archive := filepath.Join(t.TempDir(), "archived.zip")
	if _, err := ExportSessions(source, []string{"archived"}, archive); err != nil {
		t.Fatal(err)
	}
	destination := testPaths(t)
	if _, err := ImportSessions(destination, archive, ""); err != nil {
		t.Fatal(err)
	}
	threads, _ := readThreadRecords(destination.StateDB)
	row := threads["archived"]
	if row["archived"] != int64(0) || row["archived_at"] != nil || !strings.HasPrefix(databaseString(row["rollout_path"]), destination.SessionsDir) {
		t.Fatalf("archived metadata = %#v", row)
	}
}

func TestSessionArchiveRegistersRolloutsWithoutSourceDatabase(t *testing.T) {
	source := testPaths(t)
	transferTestRollout(t, source, "one", "rollout-one.jsonl", 0, nil, 1)
	archive := filepath.Join(t.TempDir(), "rollout-only.zip")
	if _, err := ExportSessions(source, []string{"one"}, archive); err != nil {
		t.Fatal(err)
	}
	destination := testPaths(t)
	db := transferTestDatabase(t, destination)
	db.Close()
	if _, err := ImportSessions(destination, archive, ""); err != nil {
		t.Fatal(err)
	}
	threads, _ := readThreadRecords(destination.StateDB)
	if threads["one"]["model"] != "source-model" {
		t.Fatalf("fallback thread = %#v", threads["one"])
	}
}

func TestSessionArchiveRollsBackDatabaseFailure(t *testing.T) {
	source := testPaths(t)
	db := transferTestDatabase(t, source)
	rollout := transferTestRollout(t, source, "one", "rollout-one.jsonl", 0, nil, 1)
	insertTransferThread(t, db, "one", rollout)
	db.Close()
	history := transferTestHistoryDatabase(t, source)
	mustExec(t, history, `INSERT INTO thread_items VALUES ('one', 'turn', 'item', 1, '{}')`)
	history.Close()
	archive := filepath.Join(t.TempDir(), "rollback.zip")
	if _, err := ExportSessions(source, []string{"one"}, archive); err != nil {
		t.Fatal(err)
	}
	destination := testPaths(t)
	history = openTestDatabase(t, filepath.Join(destination.Home, historyDBName))
	mustExec(t, history, `CREATE TABLE thread_items(thread_id TEXT, turn_id TEXT, item_id TEXT, rollout_ordinal INTEGER, item_json TEXT, incompatible TEXT NOT NULL)`)
	history.Close()
	writeFile(t, destination.SessionIndex, "original index\n", 0o600)
	if _, err := ImportSessions(destination, archive, ""); err == nil {
		t.Fatal("incompatible schema accepted")
	}
	if fileExists(destination.StateDB) || string(readFile(t, destination.SessionIndex)) != "original index\n" {
		t.Fatal("database failure left a new database or modified index")
	}
	files, _ := filepath.Glob(filepath.Join(destination.SessionsDir, "2026", "10", "05", "*"))
	if len(files) != 0 {
		t.Fatal("database failure left new rollouts")
	}
}

func TestListSessionsUsesIndexTitlesAndDatabasePrimarySegment(t *testing.T) {
	paths := testPaths(t)
	rollout := transferTestRollout(t, paths, "one", "rollout-one.jsonl", 0, nil, 1)
	db := transferTestDatabase(t, paths)
	insertTransferThread(t, db, "one", rollout)
	db.Close()
	writeJSONLines(t, paths.SessionIndex, map[string]any{"id": "one", "thread_name": "中文标题"})
	sessions, err := ListSessions(paths)
	if err != nil || len(sessions) != 1 || sessions[0].Title != "中文标题" {
		t.Fatalf("ListSessions = %#v, %v", sessions, err)
	}
	encoded, err := json.Marshal(sessions)
	if err != nil || !bytes.Contains(encoded, []byte("中文标题")) {
		t.Fatal("session descriptions cannot be encoded")
	}
}

func TestSessionArchivePreservesHistoryWithStaleByteOffsets(t *testing.T) {
	source := testPaths(t)
	root := transferTestRollout(t, source, "root", "rollout-root.jsonl", 0, nil, 1, 2)
	// Older metadata repair tools can change the first line's length without
	// updating references. The complete prefix still exists by ordinal.
	branch := transferTestRollout(t, source, "branch", "rollout-branch.jsonl", 3, &historyBase{"root", int64(len(readFile(t, root))) - 19, 3}, 4)
	archive := filepath.Join(t.TempDir(), "stale-offset.zip")
	if _, err := ExportSessions(source, []string{"branch"}, archive); err != nil {
		t.Fatal(err)
	}
	destination := testPaths(t)
	if _, err := ImportSessions(destination, archive, ""); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{root, branch} {
		relative, _ := filepath.Rel(source.Home, file)
		importedPath := filepath.Join(destination.Home, relative)
		if !sameSessionHistory(file, importedPath) || len(readFile(t, file)) != len(readFile(t, importedPath)) {
			t.Fatal("migration changed existing history or references")
		}
	}
	transferTestRollout(t, source, "missing-prefix", "rollout-missing-prefix.jsonl", 50, &historyBase{"root", int64(len(readFile(t, root))), 50}, 51)
	if _, err := ExportSessions(source, []string{"missing-prefix"}, filepath.Join(t.TempDir(), "missing.zip")); err == nil {
		t.Fatal("missing referenced ordinal was accepted")
	}
}

func TestSessionArchiveRejectsDestinationSymlinks(t *testing.T) {
	source := testPaths(t)
	transferTestRollout(t, source, "one", "rollout-one.jsonl", 0, nil, 1)
	archive := filepath.Join(t.TempDir(), "symlink-destination.zip")
	if _, err := ExportSessions(source, []string{"one"}, archive); err != nil {
		t.Fatal(err)
	}
	destination := testPaths(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, destination.SessionsDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ImportSessions(destination, archive, ""); err == nil {
		t.Fatal("destination symlink accepted")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 || fileExists(destination.SessionIndex) {
		t.Fatal("import wrote through a symlink")
	}
}

func TestSessionArchiveRollsBackExistingDatabaseTransaction(t *testing.T) {
	source := testPaths(t)
	db := transferTestDatabase(t, source)
	rollout := transferTestRollout(t, source, "new", "rollout-new.jsonl", 0, nil, 1)
	insertTransferThread(t, db, "new", rollout)
	db.Close()
	history := transferTestHistoryDatabase(t, source)
	mustExec(t, history, `INSERT INTO thread_items VALUES ('new', 'turn', 'item', 1, '{}')`)
	history.Close()
	archive := filepath.Join(t.TempDir(), "existing-rollback.zip")
	if _, err := ExportSessions(source, []string{"new"}, archive); err != nil {
		t.Fatal(err)
	}
	destination := testPaths(t)
	db = transferTestDatabase(t, destination)
	local := transferTestRollout(t, destination, "local", "rollout-local.jsonl", 0, nil, 1)
	insertTransferThread(t, db, "local", local)
	db.Close()
	history = openTestDatabase(t, filepath.Join(destination.Home, historyDBName))
	mustExec(t, history, `CREATE TABLE thread_items(thread_id TEXT, turn_id TEXT, item_id TEXT, rollout_ordinal INTEGER, item_json TEXT, required_new_column TEXT NOT NULL)`)
	history.Close()
	if _, err := ImportSessions(destination, archive, ""); err == nil {
		t.Fatal("incompatible history schema accepted")
	}
	threads, err := readThreadRecords(destination.StateDB)
	if err != nil || len(threads) != 1 || threads["local"] == nil {
		t.Fatalf("local database transaction was not rolled back: %v, %v", threads, err)
	}
}
