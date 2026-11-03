package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// The command palette replaces long rows of key hints: every action of the
// current screen is one search away, with its shortcut shown next to it so
// frequent users learn the keys naturally. Ctrl+K opens it on the host
// screen; in a session every Ctrl and F key belongs to the remote program,
// so it is Esc followed by K.

const paletteVisibleRows = 10

type paletteAction struct {
	title    string
	shortcut string
	group    string
	run      func(m *Model) (tea.Model, tea.Cmd)
}

const (
	paletteGroupServer  = "Сервер"
	paletteGroupSession = "Сессия"
	paletteGroupGeneral = "Общее"
	paletteGroupApp     = "Приложение"
)

func withGroup(group string, actions []paletteAction) []paletteAction {
	for index := range actions {
		actions[index].group = group
	}
	return actions
}

type commandPalette struct {
	title   string
	input   textinput.Model
	cursor  int
	actions []paletteAction
}

func (m *Model) openCommandPalette() {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "Что сделать?"
	input.CharLimit = 64
	input.Focus()
	m.palette = &commandPalette{title: "Команды", input: input, actions: m.paletteActions()}
	m.escapePending = false
}

func (m *Model) paletteActions() []paletteAction {
	keys := currentPlatformKeys()
	inSession := m.page == pageConnected
	if inSession {
		// In a session the Ctrl shortcuts reach the server; the manager's
		// own commands are typed after Esc.
		keys.newTab, keys.closeTab, keys.tabs, keys.quit = "Esc t", "Esc w", "Esc →", ""
		keys.toggleSFTP, keys.ports = "Esc b", "Esc p"
	}
	themeTitle := "Сменить тему · сейчас " + currentThemeName(m.config.Theme)
	common := []paletteAction{
		{title: "Новая вкладка", shortcut: keys.newTab, run: func(m *Model) (tea.Model, tea.Cmd) { m.openTab(); return m, nil }},
	}
	if len(m.tabs) > 1 {
		common = append(common,
			paletteAction{title: "Следующая вкладка", shortcut: keys.tabs, run: func(m *Model) (tea.Model, tea.Cmd) {
				m.switchTab((m.activeTab + 1) % len(m.tabs))
				return m, nil
			}},
			paletteAction{title: "Закрыть вкладку", shortcut: keys.closeTab, run: func(m *Model) (tea.Model, tea.Cmd) { m.closeActiveTab(); return m, nil }},
		)
	}
	common = append(common,
		paletteAction{title: themeTitle, run: func(m *Model) (tea.Model, tea.Cmd) { m.cycleTheme(); return m, nil }},
		paletteAction{title: "Выйти из приложения", shortcut: keys.quit, run: func(m *Model) (tea.Model, tea.Cmd) { m.close(); return m, tea.Quit }},
	)

	if m.page == pageConnected {
		files := "Показать файлы (SFTP)"
		if !m.sftpHidden {
			files = "Скрыть файлы"
		}
		actions := []paletteAction{}
		if len(m.paneTabs) < maxPanes {
			actions = append(actions, paletteAction{title: "Разделить экран: добавить терминал", shortcut: "Esc s", run: func(m *Model) (tea.Model, tea.Cmd) {
				m.openSplitPicker()
				return m, nil
			}})
		}
		if m.paneIndex(m.tabID) >= 0 && len(m.paneTabs) >= 2 {
			zoom := "Развернуть панель на весь экран"
			if m.zoomedTab == m.tabID {
				zoom = "Вернуть разделённый экран"
			}
			actions = append(actions,
				paletteAction{title: zoom, shortcut: "Esc z", run: func(m *Model) (tea.Model, tea.Cmd) { m.toggleZoom(); return m, nil }},
				paletteAction{title: "Убрать панель с экрана (сессия останется во вкладке)", run: func(m *Model) (tea.Model, tea.Cmd) {
					m.detachPane()
					return m, nil
				}},
			)
		}
		actions = append(actions, paletteAction{title: files, shortcut: keys.toggleSFTP, run: func(m *Model) (tea.Model, tea.Cmd) {
			if m.inSplit(m.tabID) {
				m.zoomedTab = m.tabID
			}
			m.toggleSFTP()
			m.resizePanes()
			return m, nil
		}})
		if !m.sftpHidden {
			focus := "Перейти к файлам"
			if m.filesFocused {
				focus = "Вернуться в терминал"
			}
			actions = append(actions,
				paletteAction{title: focus, shortcut: keys.focus, run: func(m *Model) (tea.Model, tea.Cmd) {
					m.filesFocused = !m.filesFocused
					return m, nil
				}},
				paletteAction{title: "Загрузить файлы на сервер", shortcut: keys.upload, run: func(m *Model) (tea.Model, tea.Cmd) {
					m.filesFocused = true
					return m, m.openUploadPicker()
				}},
				paletteAction{title: "Скачать выбранное", shortcut: keys.download, run: func(m *Model) (tea.Model, tea.Cmd) {
					m.filesFocused = true
					return m, m.openDownloadPicker()
				}},
				paletteAction{title: "Обновить список файлов", shortcut: "F5", run: func(m *Model) (tea.Model, tea.Cmd) {
					m.filesFocused = true
					return m, m.reloadSelectedDirectory()
				}},
				paletteAction{title: "Новая папка", shortcut: "n", run: func(m *Model) (tea.Model, tea.Cmd) {
					m.filesFocused = true
					return m, m.openSFTPAction(sftpActionMkdir)
				}},
				paletteAction{title: "Копировать на сервере", shortcut: "c", run: func(m *Model) (tea.Model, tea.Cmd) {
					m.filesFocused = true
					return m, m.openSFTPAction(sftpActionCopy)
				}},
				paletteAction{title: "Переместить или переименовать", shortcut: "m", run: func(m *Model) (tea.Model, tea.Cmd) {
					m.filesFocused = true
					return m, m.openSFTPAction(sftpActionMove)
				}},
				paletteAction{title: "Удалить файл или папку", shortcut: "Del", run: func(m *Model) (tea.Model, tea.Cmd) {
					m.filesFocused = true
					return m, m.openSFTPAction(sftpActionDelete)
				}},
			)
		}
		actions = append(actions,
			paletteAction{title: "Пробросы портов", shortcut: keys.ports, run: func(m *Model) (tea.Model, tea.Cmd) {
				m.forwardModal = &forwardModal{}
				return m, nil
			}},
			paletteAction{title: "Копировать текст терминала", shortcut: keys.copy, run: func(m *Model) (tea.Model, tea.Cmd) {
				m.filesFocused = false
				return m.handleConnectedKey(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: platformCopyMod()}))
			}},
			paletteAction{title: "Вставить из буфера обмена", shortcut: "Shift+Insert", run: func(m *Model) (tea.Model, tea.Cmd) {
				m.filesFocused = false
				return m, m.requestClipboardPaste()
			}},
			paletteAction{title: "Отключиться", shortcut: "Esc q", run: func(m *Model) (tea.Model, tea.Cmd) {
				m.disconnect("Отключено")
				return m, nil
			}},
		)
		return append(withGroup(paletteGroupSession, actions), withGroup(paletteGroupApp, common)...)
	}

	var actions []paletteAction
	if row, ok := m.selectedLaunchRow(); ok {
		target := row.session.Name
		if row.kind != launchSaved {
			target = row.session.Host
		}
		actions = append(actions, paletteAction{title: "Подключиться к " + target, shortcut: "Enter", run: func(m *Model) (tea.Model, tea.Cmd) {
			return m, m.connectLaunchRow()
		}})
		if row.kind == launchSaved {
			actions = append(actions,
				paletteAction{title: "Изменить «" + target + "»", shortcut: "Ctrl+E", run: func(m *Model) (tea.Model, tea.Cmd) { m.editSelectedSession(); return m, nil }},
				paletteAction{title: "Переместить в группу", shortcut: "Ctrl+G", run: func(m *Model) (tea.Model, tea.Cmd) { m.moveSelectedSessionToGroup(); return m, nil }},
				paletteAction{title: "Удалить «" + target + "»", shortcut: "Del", run: func(m *Model) (tea.Model, tea.Cmd) { m.askDeleteSelectedSession(); return m, nil }},
			)
		} else {
			actions = append(actions, paletteAction{title: "Сохранить " + target + " без подключения", run: func(m *Model) (tea.Model, tea.Cmd) {
				m.saveSelectedRow()
				return m, nil
			}})
		}
	}
	actions = withGroup(paletteGroupServer, actions)
	actions = append(actions, withGroup(paletteGroupGeneral, []paletteAction{
		{title: "Новый сервер", shortcut: "Ctrl+N", run: func(m *Model) (tea.Model, tea.Cmd) { m.openNewSessionForm(); return m, nil }},
		{title: "Профили пользователей", run: func(m *Model) (tea.Model, tea.Cmd) {
			m.profileManager = &profileManager{}
			return m, nil
		}},
		{title: "Импорт из истории с настройкой групп", run: func(m *Model) (tea.Model, tea.Cmd) {
			return m, m.openHistoryImport()
		}},
	})...)
	return append(actions, withGroup(paletteGroupApp, common)...)
}

func (p *commandPalette) filtered() []paletteAction {
	query := strings.TrimSpace(p.input.Value())
	if query == "" {
		return p.actions
	}
	type scored struct {
		action paletteAction
		score  int
	}
	var matches []scored
	for _, action := range p.actions {
		if score := fuzzyScore(query, action.title); score > 0 {
			matches = append(matches, scored{action, score})
		}
	}
	// Stable insertion sort keeps the curated order among equal scores.
	for i := 1; i < len(matches); i++ {
		for j := i; j > 0 && matches[j].score > matches[j-1].score; j-- {
			matches[j], matches[j-1] = matches[j-1], matches[j]
		}
	}
	result := make([]paletteAction, len(matches))
	for i, match := range matches {
		result[i] = match.action
	}
	return result
}

func (m *Model) handlePaletteKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	palette := m.palette
	actions := palette.filtered()
	switch key.String() {
	case "esc", "ctrl+k", "f1":
		m.palette = nil
		return m, nil
	case "up", "ctrl+p":
		palette.cursor = max(0, palette.cursor-1)
		return m, nil
	case "down", "ctrl+n":
		palette.cursor = min(max(0, len(actions)-1), palette.cursor+1)
		return m, nil
	case "enter":
		if len(actions) == 0 {
			return m, nil
		}
		action := actions[clamp(palette.cursor, 0, len(actions)-1)]
		m.palette = nil
		return action.run(m)
	}
	before := palette.input.Value()
	var cmd tea.Cmd
	palette.input, cmd = palette.input.Update(key)
	if palette.input.Value() != before {
		palette.cursor = 0
	}
	return m, cmd
}

func (m *Model) paletteView() string {
	palette := m.palette
	boxWidth := min(64, max(36, m.width-4))
	inner := boxWidth - 6
	styleSearch(&palette.input, inner-3)
	actions := palette.filtered()
	palette.cursor = clamp(palette.cursor, 0, max(0, len(actions)-1))
	grouped := strings.TrimSpace(palette.input.Value()) == ""

	lines := []string{
		titleStyle.Render(palette.title),
		"",
		lipgloss.NewStyle().Foreground(accentAlt).Bold(true).Render("›") + "  " + palette.input.View(),
		lipgloss.NewStyle().Foreground(borderMuted).Render(strings.Repeat("─", inner)),
	}
	start := 0
	if palette.cursor >= paletteVisibleRows {
		start = palette.cursor - paletteVisibleRows + 1
	}
	previousGroup := ""
	for index := start; index < min(len(actions), start+paletteVisibleRows); index++ {
		action := actions[index]
		if grouped && action.group != previousGroup {
			if previousGroup != "" {
				lines = append(lines, "")
			}
			lines = append(lines, lipgloss.NewStyle().Foreground(muted).Bold(true).Render(" "+strings.ToUpper(action.group)))
			previousGroup = action.group
		}
		shortcut := ellipsize(action.shortcut, inner/2) + " "
		title := truncate(action.title, inner-2-lipgloss.Width(shortcut)-1)
		gap := max(1, inner-1-lipgloss.Width(title)-lipgloss.Width(shortcut))
		if index == palette.cursor {
			lines = append(lines, lipgloss.NewStyle().Background(accent).Foreground(contrastText).Bold(true).
				Render(" "+title+strings.Repeat(" ", gap)+shortcut))
			continue
		}
		lines = append(lines, " "+lipgloss.NewStyle().Foreground(textColor).Render(title)+strings.Repeat(" ", gap)+
			lipgloss.NewStyle().Foreground(muted).Render(shortcut))
	}
	if len(actions) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(muted).Render(" Нет такой команды"))
	}
	// Keep the box from shrinking while typing so it does not jump around.
	for minimum := 4 + min(paletteVisibleRows, len(palette.actions)); len(lines) < minimum; {
		lines = append(lines, "")
	}
	box := borderStyle.Copy().BorderForeground(accent).Padding(1, 2).Width(boxWidth).Render(strings.Join(lines, "\n"))
	return m.placeCentered(box)
}
