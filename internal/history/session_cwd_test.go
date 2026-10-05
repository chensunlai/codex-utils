package history

import (
	"bytes"
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
