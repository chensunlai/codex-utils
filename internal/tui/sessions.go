package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/chensunlai/codex-utils/internal/history"
)

func (m model) handleSessionKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "q":
		m.screen = menuScreen
		m.cursor = 0
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.sessions)-1 {
			m.cursor++
		}
	case " ", "space":
		if len(m.sessions) > 0 {
			id := m.sessions[m.cursor].ID
			m.selected[id] = !m.selected[id]
		}
	case "a":
		selectAll := len(m.selectedSessionIDs()) != len(m.sessions)
		for _, session := range m.sessions {
			m.selected[session.ID] = selectAll
		}
	case "enter":
		if len(m.sessions) == 0 {
			return m, nil
		}
		if len(m.selectedSessionIDs()) == 0 {
			m.selected[m.sessions[m.cursor].ID] = true
		}
		m.screen = pathScreen
		m.pathPurpose = exportAction
		m.pathStep = 0
		directory, err := os.Getwd()
		if err != nil {
			directory = m.paths.Home
		}
		m.pathValue = []rune(filepath.Join(directory, "codex-sessions-"+time.Now().Format("20060102-150405")+".zip"))
	}
	return m, nil
}

func (m model) selectedSessionIDs() []string {
	var ids []string
	for id, selected := range m.selected {
		if selected {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func (m model) handlePathKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		if m.pathPurpose == exportAction {
			m.screen = sessionsScreen
		} else {
			m.screen = menuScreen
			m.cursor = 0
		}
	case "ctrl+u":
		m.pathValue = nil
	case "backspace", "ctrl+h":
		if len(m.pathValue) > 0 {
			m.pathValue = m.pathValue[:len(m.pathValue)-1]
		}
	case "enter":
		value := strings.TrimSpace(string(m.pathValue))
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		if value == "" && (m.pathPurpose == exportAction || m.pathStep == 0) {
			return m, nil
		}
		if m.pathPurpose == exportAction {
			m.busy = true
			m.screen = menuScreen
			m.cursor = 0
			m.messageError = false
			m.messageTitle = translate(m.language, "导出", "Export")
			m.message = translate(m.language, "正在打包所选对话及历史依赖...", "Packing selected conversations and history dependencies...")
			return m, exportCommand(m.paths, m.selectedSessionIDs(), value, m.language)
		}
		if m.pathStep == 0 {
			m.pendingZIP = value
			m.pathStep = 1
			m.pathValue = nil
		} else {
			m.pendingCwd = value
			m.pendingAction = importAction
			m.screen = confirmScreen
			m.cursor = 0
		}
	default:
		if key.Type == tea.KeyRunes {
			m.pathValue = append(m.pathValue, key.Runes...)
		} else if key.Type == tea.KeySpace {
			m.pathValue = append(m.pathValue, ' ')
		}
	}
	return m, nil
}

func (m model) sessionSelectionView(width int) string {
	var body strings.Builder
	body.WriteString(titleStyle.Render(translate(m.language, "选择要导出的对话", "Select conversations to export")) + "\n\n")
	if len(m.sessions) == 0 {
		body.WriteString(translate(m.language, "没有找到对话。按 Esc 返回。", "No conversations found. Press Esc to go back."))
		return body.String()
	}
	start, end := visibleRange(m.cursor, len(m.sessions), (m.height-13)/2)
	for i := start; i < end; i++ {
		session := m.sessions[i]
		prefix := "  [ ] "
		if m.selected[session.ID] {
			prefix = "  [x] "
		}
		if i == m.cursor {
			prefix = ">" + prefix[1:]
		}
		title := strings.Join(strings.Fields(session.Title), " ")
		line := prefix + ansi.Truncate(title, max(0, width-6), "…")
		if i == m.cursor {
			line = activeStyle.Render(line)
		}
		body.WriteString(line + "\n")
		body.WriteString(mutedStyle.Render("      "+compactPath(session.ID+"  "+session.UpdatedAt, width-6)) + "\n")
	}
	body.WriteString("\n" + translate(m.language, "fork 所需的源会话会一同导出。", "Source conversations required by forks are included."))
	body.WriteString("\n" + mutedStyle.Render(translate(m.language, "Space 勾选  a 全选/清空  Enter 继续  Esc 返回", "Space select  a all/none  Enter continue  Esc back")))
	return body.String()
}

func (m model) pathInputView(width int) string {
	title := translate(m.language, "输入导出 ZIP 的保存路径", "Enter the output ZIP path")
	help := translate(m.language, "Enter 导出  Ctrl+u 清空  Esc 返回", "Enter export  Ctrl+u clear  Esc back")
	if m.pathPurpose == importAction {
		if m.pathStep == 0 {
			title = translate(m.language, "输入要导入的 ZIP 路径", "Enter the ZIP path to import")
			help = translate(m.language, "Enter 继续  Ctrl+u 清空  Esc 返回", "Enter continue  Ctrl+u clear  Esc back")
		} else {
			title = translate(m.language, "本机工作目录（可选）", "Local working directory (optional)")
			help = translate(m.language, "留空使用本机 ~；Enter 继续  Esc 返回", "Leave empty to use local ~; Enter continue  Esc back")
		}
	}
	return titleStyle.Render(title) + "\n\n" + compactPath(string(m.pathValue), width-4) + "_\n\n" + mutedStyle.Render(help)
}

func listSessionsCommand(paths history.Paths, selectedLanguage language) tea.Cmd {
	return func() tea.Msg {
		sessions, err := history.ListSessions(paths)
		return resultMsg{title: translate(selectedLanguage, "对话", "Conversations"), action: exportAction, sessions: sessions, err: err}
	}
}

func exportCommand(paths history.Paths, ids []string, output string, selectedLanguage language) tea.Cmd {
	return func() tea.Msg {
		stats, err := history.ExportSessions(paths, ids, output)
		body := fmt.Sprintf("ZIP: %s\nSelected: %d\nHistory dependencies: %d\nRollout files: %d", stats.Path, stats.Selected, stats.Dependencies, stats.Files)
		if selectedLanguage == chinese {
			body = fmt.Sprintf("ZIP：%s\n所选对话：%d\n历史依赖：%d\n会话文件：%d", stats.Path, stats.Selected, stats.Dependencies, stats.Files)
		}
		return resultMsg{title: translate(selectedLanguage, "导出完成", "Export complete"), body: body, action: exportAction, err: err}
	}
}

func importCommand(paths history.Paths, archive, cwd string, selectedLanguage language) tea.Cmd {
	return func() tea.Msg {
		stats, err := history.ImportSessions(paths, archive, cwd)
		body := fmt.Sprintf("Added: %d\nAlready present: %d\nProvider adapted: %d\nHistory dependencies: %d", stats.Added, stats.Skipped, stats.Adapted, stats.Dependencies)
		if selectedLanguage == chinese {
			body = fmt.Sprintf("已添加：%d\n历史已存在：%d\n已适配本机 provider：%d\n历史依赖：%d", stats.Added, stats.Skipped, stats.Adapted, stats.Dependencies)
		}
		body += "\n" + translate(selectedLanguage, "导入后重新启动 Codex，再打开对话。", "Restart Codex after importing, then open the conversation.")
		return resultMsg{title: translate(selectedLanguage, "导入完成", "Import complete"), body: body, action: importAction, err: err}
	}
}
