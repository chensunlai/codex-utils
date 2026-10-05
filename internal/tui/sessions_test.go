package tui

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/chensunlai/codex-utils/internal/history"
)

func TestSessionSelectionSupportsMultipleConversationsAndKeepsSelectionOnBack(t *testing.T) {
	m := modelWithInspection()
	m.screen = sessionsScreen
	m.selected = make(map[string]bool)
	m.sessions = []history.Session{{ID: "one", Title: "第一个对话"}, {ID: "two", Title: "Second conversation"}, {ID: "three", Title: "Third"}}
	for _, key := range []string{"space", "down", "space"} {
		updated, _ := m.handleSessionKey(key)
		m = updated.(model)
	}
	if !reflect.DeepEqual(m.selectedSessionIDs(), []string{"one", "two"}) {
		t.Fatalf("selection = %v", m.selectedSessionIDs())
	}
	updated, _ := m.handleSessionKey("enter")
	m = updated.(model)
	if m.screen != pathScreen || m.pathPurpose != exportAction || !strings.HasSuffix(string(m.pathValue), ".zip") {
		t.Fatalf("export path model = %#v", m)
	}
	updated, _ = m.handlePathKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(model)
	if m.screen != sessionsScreen || len(m.selectedSessionIDs()) != 2 {
		t.Fatal("returning to selection lost checked conversations")
	}
	for _, count := range []int{3, 0} {
		updated, _ = m.handleSessionKey("a")
		m = updated.(model)
		if len(m.selectedSessionIDs()) != count {
			t.Fatalf("select all toggle = %v", m.selectedSessionIDs())
		}
	}
}

func TestSessionSelectionKeepsLongChineseTitlesOnOneLine(t *testing.T) {
	m := modelWithInspection()
	m.screen = sessionsScreen
	m.sessions = []history.Session{{ID: "one", Title: "帮我分析我的C盘目录\n" + strings.Repeat("哪些东西占据的比较多\t", 20)}}
	for _, width := range []int{32, 80} {
		view := ansi.Strip(m.sessionSelectionView(width))
		var title string
		for _, line := range strings.Split(view, "\n") {
			if strings.HasPrefix(line, "> [ ] ") {
				title = line
			}
		}
		if !strings.Contains(title, "帮我分析") || !strings.HasSuffix(title, "…") || ansi.StringWidth(title) > width {
			t.Fatalf("title wraps at width %d: %q", width, title)
		}
	}
}

func TestImportPathInputAcceptsWindowsPathsAndRequiresConfirmation(t *testing.T) {
	m := modelWithInspection()
	m.screen = pathScreen
	m.pathPurpose = importAction
	zipPath := `C:\Users\user\我的对话 q.zip`
	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(`"` + zipPath + `"`)})
	m = updated.(model)
	updated, command := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if command != nil || m.screen != pathScreen || m.pathStep != 1 || m.pendingZIP != zipPath {
		t.Fatalf("ZIP input = %#v", m)
	}
	updated, command = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if command != nil || m.screen != confirmScreen || m.pendingAction != importAction || m.pendingCwd != "" {
		t.Fatal("import did not wait for confirmation")
	}
	if !strings.Contains(m.View(), "已有对话不会被覆盖") {
		t.Fatal("import confirmation does not describe adding conversations")
	}
	updated, command = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = updated.(model)
	if command != nil || m.screen != menuScreen {
		t.Fatal("cancelled import started an operation")
	}
}

func TestExportHighlightedConversationWithoutCheckboxes(t *testing.T) {
	m := modelWithInspection()
	m.screen = sessionsScreen
	m.selected = make(map[string]bool)
	m.sessions = []history.Session{{ID: "one"}, {ID: "two"}}
	m.cursor = 1
	updated, _ := m.handleSessionKey("enter")
	m = updated.(model)
	if !reflect.DeepEqual(m.selectedSessionIDs(), []string{"two"}) {
		t.Fatal("Enter did not select highlighted conversation")
	}
}
