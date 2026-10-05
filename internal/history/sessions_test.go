package history

import (
	"path/filepath"
	"testing"
)

func TestListSessionsHidesInternalThreadsWithoutLosingForkDependencies(t *testing.T) {
	paths := testPaths(t)
	for _, session := range []struct {
		id, threadSource string
		source           any
	}{
		{"user", "user", "vscode"},
		{"legacy-user", "", "cli"},
		{"guardian", "guardian_review", map[string]any{"subagent": map[string]any{"other": "guardian"}}},
		{"subagent", "subagent", "cli"},
		{"legacy-subagent", "", map[string]any{"subagent": map[string]any{"thread_spawn": map[string]any{}}}},
	} {
		writeJSONLines(t, filepath.Join(paths.SessionsDir, "rollout-"+session.id+".jsonl"), map[string]any{
			"type": "session_meta", "payload": map[string]any{"id": session.id, "source": session.source, "thread_source": session.threadSource},
		})
	}
	writeJSONLines(t, paths.SessionIndex, map[string]any{"id": "user", "thread_name": "Guardian review"})
	sessions, err := ListSessions(paths)
	if err != nil || len(sessions) != 2 {
		t.Fatalf("listed sessions = %#v, %v", sessions, err)
	}
	for _, session := range sessions {
		if session.ID != "user" && session.ID != "legacy-user" {
			t.Fatalf("internal thread listed: %#v", session)
		}
	}
	// The catalog must still retain internal threads for explicit ID export
	// and fork dependencies.
	catalog, err := readSessionCatalog(paths)
	if err != nil || len(catalog) != 5 {
		t.Fatalf("full catalog = %#v, %v", catalog, err)
	}
	if _, err := ExportSessions(paths, []string{"guardian"}, filepath.Join(t.TempDir(), "internal.zip")); err != nil {
		t.Fatal(err)
	}
}

func TestListSessionsUsesDatabaseThreadSourceWhenHeaderIsLegacy(t *testing.T) {
	paths := testPaths(t)
	db := transferTestDatabase(t, paths)
	mustExec(t, db, `ALTER TABLE threads ADD COLUMN thread_source TEXT`)
	for _, id := range []string{"user", "guardian"} {
		rollout := transferTestRollout(t, paths, id, "rollout-"+id+".jsonl", 0, nil, 1)
		insertTransferThread(t, db, id, rollout)
	}
	mustExec(t, db, `UPDATE threads SET thread_source='guardian_review' WHERE id='guardian'`)
	mustExec(t, db, `UPDATE threads SET thread_source='user' WHERE id='user'`)
	db.Close()
	sessions, err := ListSessions(paths)
	if err != nil || len(sessions) != 1 || sessions[0].ID != "user" {
		t.Fatalf("database classification ignored: %#v, %v", sessions, err)
	}
}
