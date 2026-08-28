package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/chensunlai/codex-utils/internal/history"
)

func TestNewModelStartsWithLanguageSelection(t *testing.T) {
	m := newModel(history.Paths{Home: "/tmp/.codex"})
	if m.screen != languageScreen {
		t.Fatalf("screen = %v, want languageScreen", m.screen)
	}
	view := m.View()
	for _, expected := range []string{"选择语言 / Select language", "简体中文", "English"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("language view does not contain %q:\n%s", expected, view)
		}
	}
}

func TestSelectingChineseShowsTranslatedRepairMenu(t *testing.T) {
	m := modelWithInspection()
	updated, _ := m.handleLanguageKey("enter")
	selected := updated.(model)
	if selected.screen != menuScreen || selected.language != chinese {
		t.Fatalf("selected model = %#v", selected)
	}
	view := selected.View()
	for _, expected := range []string{"检查状态", "预览修复", "修复历史记录", "切换语言"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("Chinese menu does not contain %q:\n%s", expected, view)
		}
	}
}

func TestSelectingEnglishShowsEnglishRepairMenu(t *testing.T) {
	m := modelWithInspection()
	updated, _ := m.handleLanguageKey("down")
	m = updated.(model)
	updated, _ = m.handleLanguageKey("enter")
	selected := updated.(model)
	if selected.screen != menuScreen || selected.language != english {
		t.Fatalf("selected model = %#v", selected)
	}
	view := selected.View()
	for _, expected := range []string{"Inspect", "Preview repair", "Repair history", "Language"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("English menu does not contain %q:\n%s", expected, view)
		}
	}
}

func TestLanguageSelectionAcceptsNumberKeys(t *testing.T) {
	m := modelWithInspection()
	updated, command := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if command != nil {
		t.Fatal("language selection returned an unexpected command")
	}
	selected := updated.(model)
	if selected.language != english || selected.screen != menuScreen {
		t.Fatalf("selected model = %#v", selected)
	}
}

func modelWithInspection() model {
	m := newModel(history.Paths{Home: "/tmp/.codex"})
	m.busy = false
	m.inspection = history.Inspection{
		Paths:         m.paths,
		Settings:      history.ModelSettings{Provider: "openai", Model: "gpt-5"},
		ConfigFound:   true,
		DatabaseFound: true,
		SessionsFound: true,
		IndexFound:    true,
	}
	return m
}
