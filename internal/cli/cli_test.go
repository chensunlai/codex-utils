package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConversationTransferCommands(t *testing.T) {
	source := t.TempDir()
	for _, id := range []string{"one", "two"} {
		rollout := filepath.Join(source, "sessions", "rollout-"+id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(rollout), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(rollout, []byte(`{"type":"session_meta","payload":{"id":"`+id+`","cwd":"/source/project","model_provider":"openai","model":"test-model"}}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	archive := filepath.Join(t.TempDir(), "多个对话.zip")
	if code := Run([]string{"--codex-home", source, "list-sessions"}); code != 0 {
		t.Fatalf("list-sessions exit = %d", code)
	}
	if code := Run([]string{"--codex-home", source, "export", "--output", archive, "one", "two"}); code != 0 {
		t.Fatalf("export exit = %d", code)
	}
	destination := t.TempDir()
	for i := 0; i < 2; i++ {
		if code := Run([]string{"import", "--cwd", t.TempDir(), archive, "--codex-home=" + destination}); code != 0 {
			t.Fatalf("import exit = %d", code)
		}
	}
	for _, id := range []string{"one", "two"} {
		if _, err := os.Stat(filepath.Join(destination, "sessions", "rollout-"+id+".jsonl")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConversationTransferRejectsMissingArguments(t *testing.T) {
	for _, args := range [][]string{{"export"}, {"export", "-o", "unused.zip"}, {"import"}, {"import", "one.zip", "two.zip"}, {"list-sessions", "extra"}} {
		if code := Run(append([]string{"--codex-home", t.TempDir()}, args...)); code != 2 {
			t.Fatalf("Run(%v) = %d", args, code)
		}
	}
}
