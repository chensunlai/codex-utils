package history

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportRelocatesWorkingDirectoryAndRemapsForkByteOffsets(t *testing.T) {
	source := testPaths(t)
	root := transferTestRollout(t, source, "root", "rollout-root.jsonl", 0, nil, 1, 2)
	branch := transferTestRollout(t, source, "branch", "rollout-branch.jsonl", 3, &historyBase{"root", int64(len(readFile(t, root))), 3}, 4)
	leaf := transferTestRollout(t, source, "leaf", "rollout-leaf.jsonl", 5, &historyBase{"branch", int64(len(readFile(t, branch))), 5}, 6)
	archive := filepath.Join(t.TempDir(), "cwd.zip")
	if _, err := ExportSessions(source, []string{"leaf"}, archive); err != nil {
		t.Fatal(err)
	}
	destination := testPaths(t)
	cwd := filepath.Join(t.TempDir(), strings.Repeat("long-local-directory-", 30))
	if _, err := ImportSessions(destination, archive, cwd); err != nil {
		t.Fatal(err)
	}
	for _, original := range []string{root, branch, leaf} {
		imported := filepath.Join(destination.SessionsDir, "2026", "10", "05", filepath.Base(original))
		file, err := inspectSessionFile(destination.Home, imported)
		if err != nil || file.Header.Payload.Cwd != cwd {
			t.Fatalf("working directory was not relocated: %#v, %v", file, err)
		}
		before := bytes.SplitN(readFile(t, original), []byte{'\n'}, 2)[1]
		after := bytes.SplitN(readFile(t, imported), []byte{'\n'}, 2)[1]
		if !bytes.Equal(before, after) {
			t.Fatal("conversation events changed")
		}
		if base := file.Header.Payload.HistoryBase; base != nil {
			parent := filepath.Join(destination.SessionsDir, "2026", "10", "05", "rollout-"+base.ThreadID+".jsonl")
			if base.EndByteOffset != int64(len(readFile(t, parent))) {
				t.Fatalf("fork offset %d != parent size %d", base.EndByteOffset, len(readFile(t, parent)))
			}
		}
	}
	if _, err := ExportSessions(destination, []string{"leaf"}, filepath.Join(t.TempDir(), "roundtrip.zip")); err != nil {
		t.Fatalf("relocated fork cannot be exported: %v", err)
	}
	for _, newCwd := range []string{cwd, "", filepath.Join(t.TempDir(), "short")} {
		stats, err := ImportSessions(destination, archive, newCwd)
		if err != nil || stats.Added != 0 || stats.Skipped != 3 {
			t.Fatalf("re-import after relocation: %#v, %v", stats, err)
		}
	}
}

func TestImportRemapsReferencesToTheNamedSegmentWithOverlappingOrdinals(t *testing.T) {
	source := testPaths(t)
	root := transferTestRollout(t, source, "root", "rollout-root.jsonl", 0, nil, 1, 2, 3, 4)
	boundary, ok := ordinalByteBoundary(root, 3)
	if !ok {
		t.Fatal("missing root boundary")
	}
	segmentID := "11111111-2222-3333-4444-555555555555"
	segment := transferTestRollout(t, source, "root", "rollout-root_"+segmentID+".jsonl", 3, &historyBase{"root", boundary, 3}, 4)
	transferTestRollout(t, source, "leaf", "rollout-leaf.jsonl", 5, &historyBase{segmentID, int64(len(readFile(t, segment))), 5}, 6)
	archive := filepath.Join(t.TempDir(), "segments.zip")
	if _, err := ExportSessions(source, []string{"leaf"}, archive); err != nil {
		t.Fatal(err)
	}
	destination := testPaths(t)
	if _, err := ImportSessions(destination, archive, filepath.Join(t.TempDir(), strings.Repeat("long-cwd", 40))); err != nil {
		t.Fatal(err)
	}
	leaf, err := inspectSessionFile(destination.Home, filepath.Join(destination.SessionsDir, "2026", "10", "05", "rollout-leaf.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	importedSegment := filepath.Join(destination.SessionsDir, "2026", "10", "05", filepath.Base(segment))
	if leaf.Header.Payload.HistoryBase.EndByteOffset != int64(len(readFile(t, importedSegment))) {
		t.Fatal("reference was remapped to a different segment sharing the same ordinal")
	}
}

func TestImportDefaultsWorkspaceToLocalHomeAndRepairsSavedSettings(t *testing.T) {
	home := filepath.Join(t.TempDir(), "local-user")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	source := testPaths(t)
	db := transferTestDatabase(t, source)
	rollout := filepath.Join(source.SessionsDir, "rollout-one.jsonl")
	windows := `C:\Users\Chen\Documents\Codex`
	writeJSONLines(t, rollout,
		map[string]any{"type": "session_meta", "ordinal": 0, "payload": map[string]any{"id": "one", "cwd": windows, "runtime_workspace_roots": []string{windows, `D:\project`}}},
		map[string]any{"type": "turn_context", "ordinal": 1, "payload": map[string]any{"cwd": windows, "workspace_roots": []string{windows}, "model": "keep-model"}},
		map[string]any{"type": "event_msg", "ordinal": 2, "payload": map[string]any{"type": "thread_settings_applied", "thread_id": "one", "thread_settings": map[string]any{"cwd": windows, "runtime_workspace_roots": []string{windows}, "model": "keep-model"}}},
		map[string]any{"type": "response_item", "ordinal": 3, "payload": map[string]any{"role": "user", "text": "Keep this original path: " + windows}},
	)
	insertTransferThread(t, db, "one", rollout)
	db.Close()
	archive := filepath.Join(t.TempDir(), "workspace.zip")
	if _, err := ExportSessions(source, []string{"one"}, archive); err != nil {
		t.Fatal(err)
	}
	destination := testPaths(t)
	imported := filepath.Join(destination.SessionsDir, "rollout-one.jsonl")
	for _, cwd := range []string{"", filepath.Join(t.TempDir(), "explicit-workspace"), ""} {
		if _, err := ImportSessions(destination, archive, cwd); err != nil {
			t.Fatal(err)
		}
		want := cwd
		if want == "" {
			want = home
		}
		lines := bytes.Split(bytes.TrimSpace(readFile(t, imported)), []byte{'\n'})
		for i, line := range lines[:3] {
			var record map[string]any
			if err := json.Unmarshal(line, &record); err != nil {
				t.Fatal(err)
			}
			payload := record["payload"].(map[string]any)
			key := "runtime_workspace_roots"
			if i == 1 {
				key = "workspace_roots"
			}
			if i == 2 {
				payload = payload["thread_settings"].(map[string]any)
			}
			roots, ok := payload[key].([]any)
			if payload["cwd"] != want || !ok || len(roots) != 1 || roots[0] != want || !filepath.IsAbs(roots[0].(string)) {
				t.Fatalf("runtime paths remain foreign: %#v", payload)
			}
			if i > 0 && payload["model"] != "keep-model" {
				t.Fatal("other settings changed")
			}
		}
		original := bytes.Split(bytes.TrimSpace(readFile(t, rollout)), []byte{'\n'})
		if !bytes.Equal(lines[3], original[3]) {
			t.Fatal("user message was rewritten")
		}
		threads, err := readThreadRecords(destination.StateDB)
		if err != nil || threads["one"]["cwd"] != want {
			t.Fatalf("database cwd = %#v, %v", threads["one"], err)
		}
		index, _, err := readSessionIndex(destination.SessionIndex)
		if err != nil || index["one"]["cwd"] != want {
			t.Fatalf("index cwd = %#v, %v", index["one"], err)
		}
	}
}

func TestImportRemapsForkCutoffsAfterRewritingContextRecords(t *testing.T) {
	source := testPaths(t)
	root := filepath.Join(source.SessionsDir, "rollout-root.jsonl")
	writeJSONLines(t, root,
		map[string]any{"type": "session_meta", "ordinal": 0, "payload": map[string]any{"id": "root", "cwd": `C:\p`, "runtime_workspace_roots": []string{`C:\p`}}},
		map[string]any{"type": "turn_context", "ordinal": 1, "payload": map[string]any{"cwd": `C:\p`, "workspace_roots": []string{`C:\p`}}},
		map[string]any{"type": "response_item", "ordinal": 2, "payload": map[string]any{"text": "preserved prefix"}},
		map[string]any{"type": "turn_context", "ordinal": 3, "payload": map[string]any{"cwd": `C:\p`, "workspace_roots": []string{`C:\p`}}},
		map[string]any{"type": "response_item", "ordinal": 4, "payload": map[string]any{"text": "outside fork prefix"}},
	)
	boundary, ok := ordinalByteBoundary(root, 3)
	if !ok {
		t.Fatal("missing prefix")
	}
	transferTestRollout(t, source, "branch", "rollout-branch.jsonl", 3, &historyBase{"root", boundary, 3}, 4)
	archive := filepath.Join(t.TempDir(), "contexts.zip")
	if _, err := ExportSessions(source, []string{"branch"}, archive); err != nil {
		t.Fatal(err)
	}
	destination := testPaths(t)
	cwd := filepath.Join(t.TempDir(), strings.Repeat("long-directory-", 30))
	if _, err := ImportSessions(destination, archive, cwd); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(destination.SessionsDir, "rollout-root.jsonl")
	actual, ok := ordinalByteBoundary(parent, 3)
	if !ok || actual <= boundary {
		t.Fatal("context transformation did not grow the prefix")
	}
	child, err := inspectSessionFile(destination.Home, filepath.Join(destination.SessionsDir, "2026", "10", "05", "rollout-branch.jsonl"))
	if err != nil || child.Header.Payload.HistoryBase.EndByteOffset != actual {
		t.Fatalf("fork cutoff = %#v, want %d, err %v", child, actual, err)
	}
	if _, err := ExportSessions(destination, []string{"branch"}, filepath.Join(t.TempDir(), "again.zip")); err != nil {
		t.Fatal(err)
	}
}
