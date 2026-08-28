package history

import (
	"archive/tar"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestResolvePathsExpandsEnvironment(t *testing.T) {
	t.Setenv("CODEX_TEST_HOME", t.TempDir())
	paths, err := ResolvePaths("$CODEX_TEST_HOME/.codex")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(os.Getenv("CODEX_TEST_HOME"), ".codex")
	if paths.Home != want {
		t.Fatalf("Home = %q, want %q", paths.Home, want)
	}
	if paths.Config != filepath.Join(want, "config.toml") {
		t.Fatalf("Config = %q", paths.Config)
	}
}

func TestLoadModelSettings(t *testing.T) {
	directory := t.TempDir()
	config := filepath.Join(directory, "config.toml")
	writeFile(t, config, `model_provider = "anthropic"
model = "claude-sonnet"
`, 0o644)
	settings, err := LoadModelSettings(config)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Provider != "anthropic" || settings.Model != "claude-sonnet" {
		t.Fatalf("settings = %#v", settings)
	}

	missing, err := LoadModelSettings(filepath.Join(directory, "missing.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if missing.Provider != DefaultProvider || missing.Model != DefaultModel {
		t.Fatalf("default settings = %#v", missing)
	}
}

func TestSyncUpdatesDatabaseRolloutAndIndex(t *testing.T) {
	paths := testPaths(t)
	writeFile(t, paths.Config, "model_provider = \"openai\"\nmodel = \"gpt-5.1-codex\"\n", 0o644)
	rollout := filepath.Join(paths.SessionsDir, "2026", "05", "13", "rollout-test.jsonl")
	writeJSONLines(t, rollout,
		map[string]any{"type": "session_meta", "id": "session-1", "payload": map[string]any{"model_provider": "old", "model": "old-model"}},
		map[string]any{"type": "event", "message": "keep me"},
	)
	writeJSONLines(t, paths.SessionIndex,
		map[string]any{"id": "session-1", "model_provider": "old", "model": "old-model"},
	)
	database := openTestDatabase(t, paths.StateDB)
	mustExec(t, database, "CREATE TABLE threads (id TEXT PRIMARY KEY, model_provider TEXT, model TEXT)")
	mustExec(t, database, "INSERT INTO threads VALUES ('session-1', 'old', 'old-model')")
	database.Close()

	settings, err := LoadModelSettings(paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	stats, err := Sync(paths, settings, false)
	if err != nil {
		t.Fatal(err)
	}
	if stats.DBThreadsUpdated != 1 || stats.RolloutFilesUpdated != 1 || stats.IndexRowsUpdated != 1 {
		t.Fatalf("stats = %#v", stats)
	}
	if stats.BackupPath == "" || !fileExists(stats.BackupPath) {
		t.Fatalf("backup was not created: %#v", stats)
	}

	database = openTestDatabase(t, paths.StateDB)
	defer database.Close()
	var provider, model string
	if err := database.QueryRow("SELECT model_provider, model FROM threads WHERE id = 'session-1'").Scan(&provider, &model); err != nil {
		t.Fatal(err)
	}
	if provider != "openai" || model != "gpt-5.1-codex" {
		t.Fatalf("database values = %q, %q", provider, model)
	}
	rolloutRecord := readFirstJSONLine(t, rollout)
	payload := rolloutRecord["payload"].(map[string]any)
	if payload["model_provider"] != "openai" || payload["model"] != "gpt-5.1-codex" {
		t.Fatalf("rollout payload = %#v", payload)
	}
	indexRecord := readFirstJSONLine(t, paths.SessionIndex)
	if indexRecord["model_provider"] != "openai" || indexRecord["model"] != "gpt-5.1-codex" {
		t.Fatalf("index = %#v", indexRecord)
	}
}

func TestDryRunDoesNotModifyFiles(t *testing.T) {
	paths := testPaths(t)
	rollout := filepath.Join(paths.SessionsDir, "rollout-test.jsonl")
	original := `{"type":"session_meta","model_provider":"old","model":"old"}` + "\n"
	writeFile(t, rollout, original, 0o640)
	stats, err := Sync(paths, ModelSettings{Provider: "openai", Model: "gpt-5"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if stats.RolloutFilesUpdated != 1 || stats.BackupPath != "" {
		t.Fatalf("stats = %#v", stats)
	}
	if got := string(readFile(t, rollout)); got != original {
		t.Fatalf("rollout changed during dry run: %q", got)
	}
}

func TestSyncCreatesMissingIndexWithGitMetadata(t *testing.T) {
	paths := testPaths(t)
	writeFile(t, paths.Config, "model_provider = \"openai\"\nmodel = \"gpt-5.5\"\n", 0o644)
	database := openTestDatabase(t, paths.StateDB)
	mustExec(t, database, "CREATE TABLE threads (id TEXT PRIMARY KEY, title TEXT, updated_at INTEGER, archived INTEGER, model_provider TEXT, model TEXT, cwd TEXT, git_branch TEXT, git_sha TEXT, git_origin_url TEXT, rollout_path TEXT)")
	mustExec(t, database, `INSERT INTO threads VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"session-1", "Git Session", int64(1760000000), 0, "old", "old-model", "/repo/codex-utils", "main", "abc123", "git@github.com:chensunlai/codex-utils.git", "sessions/2026/05/17/rollout-session-1.jsonl")
	database.Close()

	settings, _ := LoadModelSettings(paths.Config)
	stats, err := Sync(paths, settings, false)
	if err != nil {
		t.Fatal(err)
	}
	if stats.IndexRowsUpdated != 1 {
		t.Fatalf("stats = %#v", stats)
	}
	entry := readFirstJSONLine(t, paths.SessionIndex)
	if entry["thread_name"] != "Git Session" || entry["cwd"] != "/repo/codex-utils" || entry["git_branch"] != "main" {
		t.Fatalf("index entry = %#v", entry)
	}
	wantGit := map[string]any{"branch": "main", "commit_hash": "abc123", "repository_url": "git@github.com:chensunlai/codex-utils.git"}
	if !reflect.DeepEqual(entry["git"], wantGit) {
		t.Fatalf("git = %#v, want %#v", entry["git"], wantGit)
	}
}

func TestSyncPreservesExistingIndexFieldsAndGitValues(t *testing.T) {
	paths := testPaths(t)
	writeJSONLines(t, paths.SessionIndex, map[string]any{
		"id": "session-1", "thread_name": "Existing Name", "custom_field": "keep-me", "git": map[string]any{"dirty": true},
	})
	database := openTestDatabase(t, paths.StateDB)
	mustExec(t, database, "CREATE TABLE threads (id TEXT PRIMARY KEY, title TEXT, updated_at INTEGER, archived INTEGER, model_provider TEXT, model TEXT, git_branch TEXT, git_sha TEXT)")
	mustExec(t, database, "INSERT INTO threads VALUES ('session-1', 'Database Name', 1760000000, 0, 'old', 'old-model', 'feature/history', 'def456')")
	database.Close()

	_, err := Sync(paths, ModelSettings{Provider: "openai", Model: "gpt-5.5"}, false)
	if err != nil {
		t.Fatal(err)
	}
	entry := readFirstJSONLine(t, paths.SessionIndex)
	if entry["thread_name"] != "Existing Name" || entry["custom_field"] != "keep-me" {
		t.Fatalf("index entry = %#v", entry)
	}
	git := entry["git"].(map[string]any)
	if git["dirty"] != true || git["branch"] != "feature/history" || git["commit_hash"] != "def456" {
		t.Fatalf("git = %#v", git)
	}
}

func TestSyncDoesNotCreateEmptyGitObject(t *testing.T) {
	paths := testPaths(t)
	database := openTestDatabase(t, paths.StateDB)
	mustExec(t, database, "CREATE TABLE threads (id TEXT PRIMARY KEY, title TEXT, updated_at INTEGER, archived INTEGER, model_provider TEXT, model TEXT, git_branch TEXT, git_sha TEXT, git_origin_url TEXT)")
	mustExec(t, database, "INSERT INTO threads VALUES ('session-1', 'No Git', 1760000000, 0, 'old', 'old-model', '', '', '')")
	database.Close()
	_, err := Sync(paths, ModelSettings{Provider: "openai", Model: "gpt-5.5"}, false)
	if err != nil {
		t.Fatal(err)
	}
	entry := readFirstJSONLine(t, paths.SessionIndex)
	for _, key := range []string{"git", "git_branch", "git_sha", "git_origin_url"} {
		if _, exists := entry[key]; exists {
			t.Fatalf("unexpected %s in %#v", key, entry)
		}
	}
}

func TestBackupUsesPortableNamesAndRestoreWorks(t *testing.T) {
	paths := testPaths(t)
	writeFile(t, paths.Config, "model = \"gpt-5\"\n", 0o640)
	rollout := filepath.Join(paths.SessionsDir, "2026", "05", "13", "rollout-test.jsonl")
	writeFile(t, rollout, "{}\n", 0o600)
	backup, err := CreateBackup(paths)
	if err != nil {
		t.Fatal(err)
	}
	names := archiveNames(t, backup)
	if !contains(names, "config.toml") || !contains(names, "sessions/2026/05/13/rollout-test.jsonl") {
		t.Fatalf("archive names = %#v", names)
	}
	for _, name := range names {
		if strings.Contains(name, `\`) {
			t.Fatalf("non-portable archive name: %q", name)
		}
	}
	writeFile(t, paths.Config, "model = \"changed\"\n", 0o640)
	if err := RestoreBackup(paths, backup); err != nil {
		t.Fatal(err)
	}
	if got := string(readFile(t, paths.Config)); got != "model = \"gpt-5\"\n" {
		t.Fatalf("restored config = %q", got)
	}
}

func TestRestoreRejectsUnsafeMembers(t *testing.T) {
	for _, member := range []string{"../outside.txt", `..\outside.txt`, "C:/outside.txt", "/outside.txt"} {
		t.Run(strings.ReplaceAll(member, "/", "_"), func(t *testing.T) {
			paths := testPaths(t)
			backup := filepath.Join(t.TempDir(), "bad.tar.gz")
			writeTestArchive(t, backup, member, []byte("bad"))
			err := RestoreBackup(paths, backup)
			if err == nil || !strings.Contains(err.Error(), "escapes CODEX_HOME") {
				t.Fatalf("RestoreBackup error = %v", err)
			}
		})
	}
}

func TestUpdateRolloutPreservesLFAndFollowingLines(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "rollout-test.jsonl")
	originalSecond := `{"type":"event","message":"keep"}` + "\n"
	writeFile(t, filePath, `{"type":"session_meta","model_provider":"old","model":"old"}`+"\r\n"+originalSecond, 0o640)
	changed, err := UpdateRolloutFile(filePath, ModelSettings{Provider: "openai", Model: "gpt-5"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected rollout to change")
	}
	raw := readFile(t, filePath)
	if strings.Contains(string(raw), "\r\n") {
		t.Fatalf("first line still uses CRLF: %q", raw)
	}
	if !strings.HasSuffix(string(raw), originalSecond) {
		t.Fatalf("following line changed: %q", raw)
	}
	if mode := fileMode(t, filePath); runtime.GOOS != "windows" && mode.Perm() != 0o640 {
		t.Fatalf("mode = %o", mode.Perm())
	}
}

func TestSyncIsIdempotent(t *testing.T) {
	paths := testPaths(t)
	writeJSONLines(t, paths.SessionIndex, map[string]any{"id": "one", "model_provider": "old", "model": "old"})
	settings := ModelSettings{Provider: "openai", Model: "gpt-5"}
	if _, err := Sync(paths, settings, false); err != nil {
		t.Fatal(err)
	}
	stats, err := Sync(paths, settings, true)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Changed() {
		t.Fatalf("second sync should be clean: %#v", stats)
	}
}

func testPaths(t *testing.T) Paths {
	t.Helper()
	home := t.TempDir()
	paths, err := ResolvePaths(home)
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

func openTestDatabase(t *testing.T, path string) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	return database
}

func mustExec(t *testing.T, database *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := database.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func writeJSONLines(t *testing.T, path string, records ...map[string]any) {
	t.Helper()
	var content strings.Builder
	for _, record := range records {
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		content.Write(encoded)
		content.WriteByte('\n')
	}
	writeFile(t, path, content.String(), 0o644)
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func readFirstJSONLine(t *testing.T, path string) map[string]any {
	t.Helper()
	first, _, _ := strings.Cut(string(readFile(t, path)), "\n")
	var record map[string]any
	if err := json.Unmarshal([]byte(first), &record); err != nil {
		t.Fatal(err)
	}
	return record
}

func archiveNames(t *testing.T, archivePath string) []string {
	t.Helper()
	input, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	gzipReader, err := gzip.NewReader(input)
	if err != nil {
		t.Fatal(err)
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	var names []string
	for {
		header, err := reader.Next()
		if err != nil {
			break
		}
		names = append(names, header.Name)
	}
	return names
}

func writeTestArchive(t *testing.T, archivePath, member string, content []byte) {
	t.Helper()
	output, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(output)
	writer := tar.NewWriter(gzipWriter)
	if err := writer.WriteHeader(&tar.Header{Name: member, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode()
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
