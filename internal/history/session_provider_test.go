package history

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestProviderAdaptationPreservesHeaderLengthAndOtherFields(t *testing.T) {
	for _, payload := range []string{
		`{"model_provider":"x","id":"one","cwd":"source"}`,
		`{"id":"one","model_provider":"source","cwd":"source"}`,
		`{"id":"one","cwd":"source","model_provider":"source"}`,
		`{ "model_provider" : "source" }`,
		`{"id":"one","instructions":"literal model_provider stays intact"}`,
	} {
		t.Run(payload, func(t *testing.T) {
			line := []byte(`{"type":"session_meta","ordinal":10,"payload":` + payload + "}\r\n")
			adapted, err := providerNeutralHeader(line, "a-much-longer-local-provider")
			if err != nil || len(adapted) != len(line) {
				t.Fatalf("adaptation = %s, %v", adapted, err)
			}
			var before, after map[string]any
			if json.Unmarshal(line, &before) != nil || json.Unmarshal(adapted, &after) != nil {
				t.Fatal("adapted header is invalid JSON")
			}
			delete(before["payload"].(map[string]any), "model_provider")
			want, _ := json.Marshal(before)
			got, _ := json.Marshal(after)
			if !bytes.Equal(want, got) {
				t.Fatalf("other metadata changed: %s != %s", got, want)
			}
			again, err := providerNeutralHeader(adapted, "a-much-longer-local-provider")
			if err != nil || !bytes.Equal(again, adapted) {
				t.Fatal("adaptation is not idempotent")
			}
		})
	}
	line := []byte(`{"type":"session_meta","payload":{"model_provider":"local"}}`)
	adapted, err := providerNeutralHeader(line, "local")
	if err != nil || !bytes.Equal(line, adapted) {
		t.Fatal("matching provider was changed")
	}
}

func TestImportRepairsPreviouslyImportedProvidersAndMissingTurns(t *testing.T) {
	source := testPaths(t)
	db := transferTestDatabase(t, source)
	rollout := transferTestRollout(t, source, "one", "rollout-one.jsonl", 0, nil, 1)
	insertTransferThread(t, db, "one", rollout)
	db.Close()
	history := transferTestHistoryDatabase(t, source)
	mustExec(t, history, `INSERT INTO thread_turns VALUES ('one','turn',1,'completed',100,200)`)
	mustExec(t, history, `INSERT INTO thread_items VALUES ('one','turn','item',1,'{}')`)
	history.Close()
	archive := filepath.Join(t.TempDir(), "repair.zip")
	if _, err := ExportSessions(source, []string{"one"}, archive); err != nil {
		t.Fatal(err)
	}
	destination := testPaths(t)
	writeFile(t, destination.Config, "model_provider = \"local-provider-with-a-long-name\"\n", 0o600)
	if _, err := ImportSessions(destination, archive, ""); err != nil {
		t.Fatal(err)
	}
	// Reproduce v0.2.0: original rollout provider and missing turn rows.
	imported := filepath.Join(destination.SessionsDir, "2026", "10", "05", "rollout-one.jsonl")
	writeFile(t, imported, string(readFile(t, rollout)), 0o600)
	db = openTestDatabase(t, destination.StateDB)
	mustExec(t, db, `UPDATE threads SET model_provider='source-provider' WHERE id='one'`)
	db.Close()
	history = openTestDatabase(t, filepath.Join(destination.Home, historyDBName))
	mustExec(t, history, `DELETE FROM thread_turns`)
	history.Close()
	stats, err := ImportSessions(destination, archive, "")
	if err != nil || stats.Added != 0 || stats.Skipped != 1 || stats.Adapted != 1 {
		t.Fatalf("repair import = %#v, %v", stats, err)
	}
	threads, _ := readThreadRecords(destination.StateDB)
	if threads["one"]["model_provider"] != "local-provider-with-a-long-name" {
		t.Fatal("database still has the source provider")
	}
	file, err := inspectSessionFile(destination.Home, imported)
	if err != nil || file.Header.Payload.Provider != "" || !sameSessionHistory(imported, rollout) || len(readFile(t, imported)) != len(readFile(t, rollout)) {
		t.Fatalf("rollout not adapted safely: %#v, %v", file, err)
	}
	history = openTestDatabase(t, filepath.Join(destination.Home, historyDBName))
	defer history.Close()
	var count int
	if err := history.QueryRow("SELECT COUNT(*) FROM thread_turns").Scan(&count); err != nil || count != 1 {
		t.Fatalf("missing turns not restored: %d, %v", count, err)
	}
	stats, err = ImportSessions(destination, archive, "")
	if err != nil || stats.Added != 0 || stats.Skipped != 1 || stats.Adapted != 0 {
		t.Fatalf("repeat repair = %#v, %v", stats, err)
	}
}

func TestImportLegacyArchiveInvalidatesIncompleteProjection(t *testing.T) {
	source := testPaths(t)
	transferTestRollout(t, source, "one", "rollout-one.jsonl", 0, nil, 1)
	history := transferTestHistoryDatabase(t, source)
	mustExec(t, history, `INSERT INTO thread_items VALUES ('one','turn','item',1,'{}')`)
	mustExec(t, history, `INSERT INTO thread_history_projection_state VALUES ('one',100,2)`)
	history.Close()
	archive := filepath.Join(t.TempDir(), "legacy.zip")
	if _, err := ExportSessions(source, []string{"one"}, archive); err != nil {
		t.Fatal(err)
	}
	destination := testPaths(t)
	if _, err := ImportSessions(destination, archive, ""); err != nil {
		t.Fatal(err)
	}
	history = openTestDatabase(t, filepath.Join(destination.Home, historyDBName))
	defer history.Close()
	var count int
	if err := history.QueryRow("SELECT COUNT(*) FROM thread_history_projection_state").Scan(&count); err != nil || count != 0 {
		t.Fatalf("incomplete projection was preserved: %d, %v", count, err)
	}
}
