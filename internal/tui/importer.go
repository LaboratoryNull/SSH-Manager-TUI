package tui

import (
	"fmt"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"ssh-manager-tui/internal/config"
)

func (m *Model) openHistoryImport() tea.Cmd {
	m.importWizard = &importWizard{loading: true}
	tabID := m.tabID
	return func() tea.Msg {
		sessions, err := scanSSHHistory()
		candidates := make([]importCandidate, 0, len(sessions))
		for _, session := range sessions {
			candidates = append(candidates, importCandidate{session: session})
		}
		return historyLoadedMsg{tabID: tabID, candidates: candidates, err: err}
	}
}

func (m *Model) handleHistoryLoaded(msg historyLoadedMsg) (tea.Model, tea.Cmd) {
	if m.importWizard == nil {
		return m, nil
	}
	m.importWizard.loading = false
	m.importWizard.entries = msg.candidates
	if msg.err != nil {
		m.importWizard.status = "Ошибка чтения history: " + msg.err.Error()
	} else if len(msg.candidates) == 0 {
		m.importWizard.status = "Команды SSH в history не найдены"
	} else {
		m.importWizard.status = fmt.Sprintf("Найдено подключений: %d", len(msg.candidates))
	}
	return m, nil
}

func (m *Model) handleImportKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	wizard := m.importWizard
	if wizard == nil || wizard.loading {
		if key.Keystroke() == "esc" {
			m.importWizard = nil
		}
		return m, nil
	}
	if wizard.groupInput != nil {
		switch key.Keystroke() {
		case "esc":
			wizard.groupInput = nil
			wizard.groupCursor = -1
			return m, nil
		case "down", "ctrl+n":
			suggestions := m.filteredGroupSuggestions(wizard.groupInput.Value())
			if len(suggestions) > 0 {
				wizard.groupCursor = (wizard.groupCursor + 1) % len(suggestions)
			}
			return m, nil
		case "up", "ctrl+p":
			suggestions := m.filteredGroupSuggestions(wizard.groupInput.Value())
			if len(suggestions) > 0 {
				if wizard.groupCursor < 0 {
					wizard.groupCursor = len(suggestions) - 1
				} else {
					wizard.groupCursor = (wizard.groupCursor - 1 + len(suggestions)) % len(suggestions)
				}
			}
			return m, nil
		case "tab":
			suggestions := m.filteredGroupSuggestions(wizard.groupInput.Value())
			if len(suggestions) > 0 {
				index := wizard.groupCursor
				if index < 0 || index >= len(suggestions) {
					index = 0
				}
				wizard.groupInput.SetValue(suggestions[index])
				wizard.groupInput.CursorEnd()
				wizard.groupCursor = -1
			}
			return m, nil
		case "enter":
			if wizard.cursor >= 0 && wizard.cursor < len(wizard.entries) {
				group := strings.TrimSpace(wizard.groupInput.Value())
				suggestions := m.filteredGroupSuggestions(wizard.groupInput.Value())
				if wizard.groupCursor >= 0 && wizard.groupCursor < len(suggestions) {
					group = suggestions[wizard.groupCursor]
				}
				if strings.IndexFunc(group, unicode.IsControl) >= 0 {
					wizard.status = "Название группы не должно содержать управляющие символы"
					return m, nil
				}
				wizard.entries[wizard.cursor].group = group
			}
			wizard.groupInput = nil
			wizard.groupCursor = -1
			return m, nil
		}
		var cmd tea.Cmd
		*wizard.groupInput, cmd = wizard.groupInput.Update(key)
		wizard.groupCursor = -1
		return m, cmd
	}

	switch key.Keystroke() {
	case "esc":
		m.importWizard = nil
	case "up", "k":
		if wizard.finishFocused {
			wizard.finishFocused = false
		} else {
			wizard.cursor = max(0, wizard.cursor-1)
		}
	case "down", "j":
		wizard.cursor = min(max(0, len(wizard.entries)-1), wizard.cursor+1)
		wizard.finishFocused = false
	case "tab":
		wizard.finishFocused = !wizard.finishFocused
	case "space":
		if wizard.cursor >= 0 && wizard.cursor < len(wizard.entries) {
			wizard.entries[wizard.cursor].selected = !wizard.entries[wizard.cursor].selected
		}
	case "a":
		selectAll := false
		for _, entry := range wizard.entries {
			if !entry.selected {
				selectAll = true
				break
			}
		}
		for i := range wizard.entries {
			wizard.entries[i].selected = selectAll
		}
	case "p", "right":
		m.cycleImportProfile(1)
	case "left":
		m.cycleImportProfile(-1)
	case "g":
		if wizard.cursor >= 0 && wizard.cursor < len(wizard.entries) {
			input := styledTextInput("например, Production", wizard.entries[wizard.cursor].group, 42)
			input.Focus()
			wizard.groupInput = &input
			wizard.groupCursor = -1
		}
	case "enter":
		if wizard.finishFocused {
			m.finishHistoryImport()
		} else if wizard.cursor >= 0 && wizard.cursor < len(wizard.entries) {
			wizard.entries[wizard.cursor].selected = !wizard.entries[wizard.cursor].selected
		}
	case "f":
		m.finishHistoryImport()
	}
	return m, nil
}

func (m *Model) updateImportWizard(message tea.Msg) (tea.Model, tea.Cmd) {
	if m.importWizard == nil || m.importWizard.groupInput == nil {
		return m, nil
	}
	var cmd tea.Cmd
	*m.importWizard.groupInput, cmd = m.importWizard.groupInput.Update(message)
	return m, cmd
}

func (m *Model) cycleImportProfile(direction int) {
	wizard := m.importWizard
	if wizard == nil || wizard.cursor < 0 || wizard.cursor >= len(wizard.entries) {
		return
	}
	names := []string{""}
	for _, profile := range m.config.UserProfiles {
		names = append(names, profile.Name)
	}
	current := 0
	for i, name := range names {
		if strings.EqualFold(name, wizard.entries[wizard.cursor].profile) {
			current = i
			break
		}
	}
	wizard.entries[wizard.cursor].profile = names[(current+direction+len(names))%len(names)]
}

func (m *Model) finishHistoryImport() {
	wizard := m.importWizard
	if wizard == nil {
		return
	}
	oldSessions := append([]config.Session(nil), m.config.Sessions...)
	imported, skipped := 0, 0
	for _, candidate := range wizard.entries {
		if !candidate.selected {
			continue
		}
		session := candidate.session
		session.Group = candidate.group
		session.CredentialProfile = candidate.profile
		if candidate.profile != "" {
			session.User, session.Password, session.KeyPath = "", "", ""
		}
		if m.sameImportedEndpointExists(session) {
			skipped++
			continue
		}
		session.Name = m.uniqueSessionName(session.Name)
		if _, err := m.config.ResolveSession(session); err != nil {
			wizard.status = "Не удалось импортировать " + session.Host + ": " + err.Error()
			continue
		}
		m.config.Sessions = append(m.config.Sessions, session)
		imported++
	}
	if imported == 0 {
		wizard.status = fmt.Sprintf("Нечего импортировать; пропущено: %d", skipped)
		return
	}
	if err := m.store.Save(m.config); err != nil {
		m.config.Sessions = oldSessions
		wizard.status = "Не удалось сохранить импорт: " + err.Error()
		return
	}
	m.importWizard = nil
	m.status = fmt.Sprintf("Импортировано хостов: %d", imported)
	if skipped > 0 {
		m.status += fmt.Sprintf(", уже существовало: %d", skipped)
	}
}

func (m *Model) sameImportedEndpointExists(candidate config.Session) bool {
	candidateResolved, err := m.config.ResolveSession(candidate)
	if err != nil {
		return false
	}
	for _, existing := range m.config.Sessions {
		existingResolved, resolveErr := m.config.ResolveSession(existing)
		if resolveErr == nil && strings.EqualFold(existing.Host, candidate.Host) && existing.Port == candidate.Port &&
			strings.EqualFold(existingResolved.User, candidateResolved.User) {
			return true
		}
	}
	return false
}

func (m *Model) uniqueSessionName(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "imported-host"
	}
	exists := func(name string) bool {
		for _, session := range m.config.Sessions {
			if strings.EqualFold(session.Name, name) {
				return true
			}
		}
		return false
	}
	if !exists(base) {
		return base
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s-%d", base, suffix)
		if !exists(candidate) {
			return candidate
		}
	}
}

func (m *Model) importView() string {
	wizard := m.importWizard
	if wizard == nil {
		return m.sessionsView()
	}
	boxWidth := min(104, max(46, m.width-4))
	boxHeight := min(32, max(16, m.contentHeight()-2))
	inner := max(20, boxWidth-6)
	lines := []string{titleStyle.Render("Импорт из истории"), subtitleStyle.Render("Выберите хосты и назначьте профиль/группу"), ""}
	if wizard.loading {
		lines = append(lines, lipgloss.NewStyle().Foreground(muted).Render("Чтение Bash, Zsh и Fish history…"))
	} else if wizard.groupInput != nil {
		entry := wizard.entries[wizard.cursor]
		suggestions := m.filteredGroupSuggestions(wizard.groupInput.Value())
		lines = append(lines,
			lipgloss.NewStyle().Foreground(textColor).Render(entry.session.User+"@"+entry.session.Host), "",
			lipgloss.NewStyle().Foreground(accentAlt).Bold(true).Render("Группа"), wizard.groupInput.View(), "",
		)
		for index, suggestion := range suggestions[:min(5, len(suggestions))] {
			prefix := "  "
			style := lipgloss.NewStyle().Foreground(textColor).Width(inner)
			if index == wizard.groupCursor {
				prefix = "▸ "
				style = selectedStyle.Copy().Foreground(accentAlt).Width(inner)
			}
			lines = append(lines, style.Render(truncate(prefix+suggestion, inner)))
		}
		if len(suggestions) == 0 {
			lines = append(lines, lipgloss.NewStyle().Foreground(muted).Italic(true).Render("Нет совпадений — будет создана новая группа"))
		}
		lines = append(lines, "", renderHints([2]string{"↑/↓", "выбрать"}, [2]string{"Tab", "подставить"}, [2]string{"Enter", "сохранить"}, [2]string{"Esc", "отмена"}))
	} else if len(wizard.entries) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(muted).Italic(true).Render("Подключения не найдены."))
	} else {
		listHeight := max(3, boxHeight-11)
		start := 0
		if wizard.cursor >= listHeight {
			start = wizard.cursor - listHeight + 1
		}
		end := min(len(wizard.entries), start+listHeight)
		for index := start; index < end; index++ {
			entry := wizard.entries[index]
			mark := "○"
			if entry.selected {
				mark = "✓"
			}
			profile := "вручную"
			if entry.profile != "" {
				profile = entry.profile
			}
			group := "без группы"
			if entry.group != "" {
				group = entry.group
			}
			line := fmt.Sprintf("%s %s@%s:%d  P:%s  G:%s", mark, entry.session.User, entry.session.Host, entry.session.Port, profile, group)
			style := lipgloss.NewStyle().Width(inner).Foreground(textColor)
			if index == wizard.cursor && !wizard.finishFocused {
				style = selectedStyle.Copy().Width(inner)
			}
			lines = append(lines, style.Render(truncate(line, inner)))
		}
		for len(lines) < boxHeight-7 {
			lines = append(lines, "")
		}
		lines = append(lines, controlStyle("Завершено", wizard.finishFocused), "",
			hintsView(inner,
				[][2]string{{"Space", "выбрать"}, {"p/←/→", "профиль"}, {"g", "группа"}, {"a", "все"}, {"Tab", "к Завершено"}, {"f", "импорт"}, {"Esc", "отмена"}},
				[][2]string{{"Space", "выбор"}, {"p", "профиль"}, {"g", "группа"}, {"Tab", "готово"}, {"Esc", "назад"}},
			))
	}
	if wizard.status != "" {
		lines = append(lines, "", statusLine(wizard.status))
	}
	box := borderStyle.Copy().BorderForeground(accentWarm).Padding(1, 2).Width(boxWidth - 2).Height(boxHeight - 2).Render(strings.Join(lines, "\n"))
	return m.placeCentered(box)
}
