package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/chensunlai/codex-utils/internal/buildinfo"
	"github.com/chensunlai/codex-utils/internal/history"
)

type screen int

const (
	languageScreen screen = iota
	menuScreen
	backupsScreen
	confirmScreen
)

type language int

const (
	chinese language = iota
	english
)

type action int

const (
	inspectAction action = iota
	previewAction
	repairAction
	backupAction
	restoreAction
	languageAction
	quitAction
)

type menuItem struct {
	label       string
	description string
	action      action
}

func localizedMenuItems(selected language) []menuItem {
	if selected == chinese {
		return []menuItem{
			{label: "检查状态", description: "查看路径和当前模型配置", action: inspectAction},
			{label: "预览修复", description: "扫描历史记录，不修改文件", action: previewAction},
			{label: "修复历史记录", description: "备份并同步全部历史元数据", action: repairAction},
			{label: "创建备份", description: "只备份历史记录，不进行修复", action: backupAction},
			{label: "恢复备份", description: "选择一个已有备份进行恢复", action: restoreAction},
			{label: "切换语言", description: "简体中文 / English", action: languageAction},
			{label: "退出", description: "退出 codex-utils", action: quitAction},
		}
	}
	return []menuItem{
		{label: "Inspect", description: "Show paths and active model settings", action: inspectAction},
		{label: "Preview repair", description: "Scan history without changing files", action: previewAction},
		{label: "Repair history", description: "Back up and synchronize all metadata", action: repairAction},
		{label: "Create backup", description: "Archive history without changing it", action: backupAction},
		{label: "Restore backup", description: "Choose an archive to restore", action: restoreAction},
		{label: "Language", description: "简体中文 / English", action: languageAction},
		{label: "Quit", description: "Leave codex-utils", action: quitAction},
	}
}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	mutedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	activeStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	warnStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	errorStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196"))
	panelStyle  = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("238")).Padding(1, 2)
)

type model struct {
	paths         history.Paths
	inspection    history.Inspection
	screen        screen
	language      language
	cursor        int
	width         int
	height        int
	busy          bool
	messageTitle  string
	message       string
	messageError  bool
	backups       []string
	pendingAction action
	pendingBackup string
}

type resultMsg struct {
	title      string
	body       string
	inspection history.Inspection
	backups    []string
	action     action
	err        error
}

func Run(paths history.Paths) error {
	program := tea.NewProgram(newModel(paths), tea.WithAltScreen())
	_, err := program.Run()
	return err
}

func newModel(paths history.Paths) model {
	return model{
		paths:        paths,
		screen:       languageScreen,
		language:     chinese,
		busy:         true,
		messageTitle: "正在加载",
		message:      "正在检查 Codex 历史记录...",
	}
}

func (m model) Init() tea.Cmd {
	return inspectCommand(m.paths, inspectAction, m.language)
}

func (m model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := message.(type) {
	case tea.WindowSizeMsg:
		m.width = typed.Width
		m.height = typed.Height
		return m, nil
	case resultMsg:
		m.busy = false
		m.messageError = typed.err != nil
		m.messageTitle = typed.title
		if typed.err != nil {
			m.messageTitle = translate(m.language, "错误", "Error")
			m.message = typed.err.Error()
		} else {
			if typed.inspection.Paths.Home != "" {
				m.inspection = typed.inspection
			}
			if typed.action == inspectAction && m.inspection.Paths.Home != "" {
				m.messageTitle = translate(m.language, "状态", "Status")
				m.message = formatInspection(m.inspection, m.language)
			} else {
				m.message = typed.body
			}
			if typed.action == restoreAction && typed.backups != nil {
				m.backups = typed.backups
				m.screen = backupsScreen
				m.cursor = 0
			}
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(typed)
	}
	return m, nil
}

func (m model) handleKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	keyName := key.String()
	if keyName == "ctrl+c" {
		return m, tea.Quit
	}
	if m.screen == languageScreen {
		return m.handleLanguageKey(keyName)
	}
	if m.busy {
		return m, nil
	}
	if m.screen == confirmScreen {
		switch keyName {
		case "y", "Y", "enter":
			m.busy = true
			m.messageError = false
			m.screen = menuScreen
			if m.pendingAction == repairAction {
				m.messageTitle = translate(m.language, "修复", "Repair")
				m.message = translate(m.language, "正在同步历史记录...", "Synchronizing history...")
				return m, syncCommand(m.paths, false, m.language)
			}
			m.messageTitle = translate(m.language, "恢复", "Restore")
			m.message = translate(m.language, "正在恢复所选备份...", "Restoring selected backup...")
			return m, restoreCommand(m.paths, m.pendingBackup, m.language)
		case "n", "N", "esc", "q":
			m.screen = menuScreen
			m.messageTitle = translate(m.language, "已取消", "Cancelled")
			m.message = translate(m.language, "没有修改任何文件。", "No files were changed.")
		}
		return m, nil
	}
	if m.screen == backupsScreen {
		switch keyName {
		case "esc", "q", "left", "h":
			m.screen = menuScreen
			m.cursor = 0
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.backups)-1 {
				m.cursor++
			}
		case "enter":
			if len(m.backups) > 0 {
				m.pendingAction = restoreAction
				m.pendingBackup = m.backups[m.cursor]
				m.screen = confirmScreen
			}
		}
		return m, nil
	}

	items := localizedMenuItems(m.language)
	switch keyName {
	case "q":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(items)-1 {
			m.cursor++
		}
	case "enter":
		selected := items[m.cursor].action
		switch selected {
		case quitAction:
			return m, tea.Quit
		case inspectAction:
			m.busy = true
			m.messageError = false
			m.messageTitle = translate(m.language, "检查状态", "Inspect")
			m.message = translate(m.language, "正在刷新状态...", "Refreshing status...")
			return m, inspectCommand(m.paths, inspectAction, m.language)
		case previewAction:
			m.busy = true
			m.messageError = false
			m.messageTitle = translate(m.language, "预览修复", "Preview")
			m.message = translate(m.language, "正在扫描历史记录...", "Scanning history...")
			return m, syncCommand(m.paths, true, m.language)
		case repairAction:
			m.pendingAction = repairAction
			m.screen = confirmScreen
		case backupAction:
			m.busy = true
			m.messageError = false
			m.messageTitle = translate(m.language, "创建备份", "Backup")
			m.message = translate(m.language, "正在创建备份文件...", "Creating archive...")
			return m, backupCommand(m.paths, m.language)
		case restoreAction:
			m.busy = true
			m.messageError = false
			m.messageTitle = translate(m.language, "备份", "Backups")
			m.message = translate(m.language, "正在读取备份列表...", "Loading backup list...")
			return m, listBackupsCommand(m.paths, m.language)
		case languageAction:
			m.screen = languageScreen
			m.cursor = int(m.language)
		}
	}
	return m, nil
}

func (m model) handleLanguageKey(keyName string) (tea.Model, tea.Cmd) {
	switch keyName {
	case "q":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < 1 {
			m.cursor++
		}
	case "1":
		m.cursor = 0
		return m.selectLanguage(), nil
	case "2":
		m.cursor = 1
		return m.selectLanguage(), nil
	case "enter":
		return m.selectLanguage(), nil
	}
	return m, nil
}

func (m model) selectLanguage() model {
	m.language = language(m.cursor)
	m.screen = menuScreen
	m.cursor = 0
	m.messageError = false
	if m.inspection.Paths.Home != "" {
		m.messageTitle = translate(m.language, "状态", "Status")
		m.message = formatInspection(m.inspection, m.language)
	} else {
		m.messageTitle = translate(m.language, "正在加载", "Loading")
		m.message = translate(m.language, "正在检查 Codex 历史记录...", "Inspecting Codex history...")
	}
	return m
}

func (m model) View() string {
	contentWidth := 80
	if m.width > 0 {
		contentWidth = m.width - 8
	}
	if contentWidth < 24 {
		contentWidth = 24
	}
	if contentWidth > 88 {
		contentWidth = 88
	}
	var body strings.Builder
	body.WriteString(titleStyle.Render("codex-utils"))
	body.WriteString(mutedStyle.Render("  " + buildinfo.Version))
	body.WriteString("\n")
	body.WriteString(mutedStyle.Render(compactPath(m.paths.Home, contentWidth)))
	body.WriteString("\n\n")

	switch m.screen {
	case languageScreen:
		body.WriteString(titleStyle.Render("选择语言 / Select language"))
		body.WriteString("\n\n")
		for index, option := range []string{"简体中文", "English"} {
			prefix := "  "
			style := lipgloss.NewStyle()
			if index == m.cursor {
				prefix = "> "
				style = activeStyle
			}
			body.WriteString(style.Render(prefix + option))
			body.WriteByte('\n')
		}
		body.WriteString("\n" + mutedStyle.Render("↑/↓ 选择 · Enter 确认 / select"))
	case confirmScreen:
		body.WriteString(warnStyle.Render(translate(m.language, "确认操作", "Confirm change")))
		body.WriteString("\n\n")
		if m.pendingAction == repairAction {
			body.WriteString(translate(
				m.language,
				"更新历史元数据前会自动创建备份。\n",
				"A backup will be created before history metadata is updated.\n",
			))
		} else {
			body.WriteString(translate(m.language, "恢复：", "Restore: ") + compactPath(m.pendingBackup, contentWidth-9) + "\n")
		}
		body.WriteString("\n" + translate(
			m.language,
			"按 y 或 Enter 继续；按 n 或 Esc 取消。",
			"Press y or Enter to continue; n or Esc to cancel.",
		))
	case backupsScreen:
		body.WriteString(activeStyle.Render(translate(m.language, "选择备份", "Select a backup")))
		body.WriteString("\n\n")
		if len(m.backups) == 0 {
			body.WriteString(mutedStyle.Render(translate(
				m.language,
				"没有找到备份。按 Esc 返回。",
				"No backups found. Press Esc to return.",
			)))
		} else {
			start, end := visibleRange(m.cursor, len(m.backups), m.height-11)
			for index := start; index < end; index++ {
				prefix := "  "
				style := lipgloss.NewStyle()
				if index == m.cursor {
					prefix = "> "
					style = activeStyle
				}
				body.WriteString(style.Render(prefix + filepath.Base(m.backups[index])))
				body.WriteByte('\n')
			}
			body.WriteString("\n" + mutedStyle.Render(translate(
				m.language,
				"Enter 恢复  Esc 返回",
				"Enter restore  Esc back",
			)))
		}
	default:
		for index, item := range localizedMenuItems(m.language) {
			prefix := "  "
			labelStyle := lipgloss.NewStyle()
			if index == m.cursor {
				prefix = "> "
				labelStyle = activeStyle
			}
			if contentWidth < 68 {
				body.WriteString(labelStyle.Render(prefix + item.label))
				body.WriteByte('\n')
				body.WriteString(mutedStyle.Width(contentWidth - 4).Render("    " + item.description))
				body.WriteByte('\n')
			} else {
				label := prefix + item.label
				padding := 18 - lipgloss.Width(label)
				if padding < 2 {
					padding = 2
				}
				body.WriteString(labelStyle.Render(label + strings.Repeat(" ", padding)))
				body.WriteString(mutedStyle.Render(item.description))
				body.WriteByte('\n')
			}
		}
		body.WriteString("\n")
		messageStyle := lipgloss.NewStyle()
		if m.messageError {
			messageStyle = errorStyle
		} else if m.busy {
			messageStyle = warnStyle
		} else {
			messageStyle = activeStyle
		}
		body.WriteString(messageStyle.Render(m.messageTitle))
		body.WriteString("\n")
		body.WriteString(lipgloss.NewStyle().Width(contentWidth - 4).Render(m.message))
		body.WriteString("\n\n")
		body.WriteString(mutedStyle.Render(translate(
			m.language,
			"↑/↓ 移动  Enter 选择  q 退出",
			"Up/Down navigate  Enter select  q quit",
		)))
	}

	return panelStyle.Width(contentWidth).Render(body.String())
}

func inspectCommand(paths history.Paths, selected action, selectedLanguage language) tea.Cmd {
	return func() tea.Msg {
		inspection, err := history.Inspect(paths)
		return resultMsg{
			title:      translate(selectedLanguage, "状态", "Status"),
			body:       formatInspection(inspection, selectedLanguage),
			inspection: inspection,
			action:     selected,
			err:        err,
		}
	}
}

func syncCommand(paths history.Paths, dryRun bool, selectedLanguage language) tea.Cmd {
	return func() tea.Msg {
		settings, err := history.LoadModelSettings(paths.Config)
		if err != nil {
			return resultMsg{action: previewAction, err: err}
		}
		stats, err := history.Sync(paths, settings, dryRun)
		title := translate(selectedLanguage, "修复完成", "Repair complete")
		actionName := repairAction
		if dryRun {
			title = translate(selectedLanguage, "预览完成", "Preview complete")
			actionName = previewAction
		}
		return resultMsg{
			title:  title,
			body:   formatStats(settings, stats, dryRun, selectedLanguage),
			action: actionName,
			err:    err,
		}
	}
}

func backupCommand(paths history.Paths, selectedLanguage language) tea.Cmd {
	return func() tea.Msg {
		backup, err := history.CreateBackup(paths)
		return resultMsg{
			title:  translate(selectedLanguage, "备份已创建", "Backup created"),
			body:   backup,
			action: backupAction,
			err:    err,
		}
	}
}

func listBackupsCommand(paths history.Paths, selectedLanguage language) tea.Cmd {
	return func() tea.Msg {
		backups, err := history.ListBackups(paths)
		for left, right := 0, len(backups)-1; left < right; left, right = left+1, right-1 {
			backups[left], backups[right] = backups[right], backups[left]
		}
		return resultMsg{
			title:   translate(selectedLanguage, "备份", "Backups"),
			backups: backups,
			action:  restoreAction,
			err:     err,
		}
	}
}

func restoreCommand(paths history.Paths, backup string, selectedLanguage language) tea.Cmd {
	return func() tea.Msg {
		err := history.RestoreBackup(paths, backup)
		return resultMsg{
			title:  translate(selectedLanguage, "恢复完成", "Restore complete"),
			body:   backup,
			action: restoreAction,
			err:    err,
		}
	}
}

func formatInspection(inspection history.Inspection, selectedLanguage language) string {
	if selectedLanguage == chinese {
		return fmt.Sprintf(
			"服务商：%s\n模型：%s\n配置文件：%s\n状态数据库：%s\n会话目录：%s\n会话索引：%s\n备份数量：%d",
			inspection.Settings.Provider,
			inspection.Settings.Model,
			found(inspection.ConfigFound, selectedLanguage),
			found(inspection.DatabaseFound, selectedLanguage),
			found(inspection.SessionsFound, selectedLanguage),
			found(inspection.IndexFound, selectedLanguage),
			inspection.BackupCount,
		)
	}
	return fmt.Sprintf(
		"Provider: %s\nModel:    %s\nConfig:   %s\nDatabase: %s\nSessions: %s\nIndex:    %s\nBackups:  %d",
		inspection.Settings.Provider,
		inspection.Settings.Model,
		found(inspection.ConfigFound, selectedLanguage),
		found(inspection.DatabaseFound, selectedLanguage),
		found(inspection.SessionsFound, selectedLanguage),
		found(inspection.IndexFound, selectedLanguage),
		inspection.BackupCount,
	)
}

func formatStats(settings history.ModelSettings, stats history.Stats, dryRun bool, selectedLanguage language) string {
	if selectedLanguage == chinese {
		mode := "已同步"
		if dryRun {
			mode = "将更新"
		}
		result := fmt.Sprintf(
			"%s：%s / %s\n数据库会话：%d/%d\n会话文件：%d/%d\n索引记录：%d/%d",
			mode,
			settings.Provider,
			settings.Model,
			stats.DBThreadsUpdated,
			stats.DBThreadsSeen,
			stats.RolloutFilesUpdated,
			stats.RolloutFilesSeen,
			stats.IndexRowsUpdated,
			stats.IndexRowsSeen,
		)
		if stats.MalformedJSONLines > 0 {
			result += fmt.Sprintf("\n已跳过格式错误的索引记录：%d", stats.MalformedJSONLines)
		}
		if stats.BackupPath != "" {
			result += "\n备份：" + stats.BackupPath
		}
		if dryRun && !stats.Changed() {
			result += "\n不需要修改。"
		}
		return result
	}

	mode := "Synchronized"
	if dryRun {
		mode = "Would update"
	}
	result := fmt.Sprintf(
		"%s %s / %s\nDatabase threads: %d/%d\nRollout files:    %d/%d\nIndex rows:       %d/%d",
		mode,
		settings.Provider,
		settings.Model,
		stats.DBThreadsUpdated,
		stats.DBThreadsSeen,
		stats.RolloutFilesUpdated,
		stats.RolloutFilesSeen,
		stats.IndexRowsUpdated,
		stats.IndexRowsSeen,
	)
	if stats.MalformedJSONLines > 0 {
		result += fmt.Sprintf("\nMalformed index lines skipped: %d", stats.MalformedJSONLines)
	}
	if stats.BackupPath != "" {
		result += "\nBackup: " + stats.BackupPath
	}
	if dryRun && !stats.Changed() {
		result += "\nNo changes needed."
	}
	return result
}

func found(value bool, selectedLanguage language) string {
	if value {
		return translate(selectedLanguage, "已找到", "found")
	}
	return translate(selectedLanguage, "未找到", "missing")
}

func translate(selectedLanguage language, chineseText, englishText string) string {
	if selectedLanguage == chinese {
		return chineseText
	}
	return englishText
}

func compactPath(value string, width int) string {
	runes := []rune(value)
	if width < 8 || len(runes) <= width {
		return value
	}
	return "..." + string(runes[len(runes)-(width-3):])
}

func visibleRange(cursor, total, available int) (int, int) {
	if available < 3 {
		available = 3
	}
	if available > total {
		available = total
	}
	start := cursor - available/2
	if start < 0 {
		start = 0
	}
	if start+available > total {
		start = total - available
	}
	return start, start + available
}
