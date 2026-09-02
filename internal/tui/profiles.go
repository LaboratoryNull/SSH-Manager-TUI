package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"ssh-manager-tui/internal/config"
)

type profileManager struct {
	cursor int
	editor *profileEditor
	status string
}

type profileEditor struct {
	inputs    []textinput.Model
	focus     int
	editIndex int
	original  string
}

func newProfileEditor(profile *config.UserProfile, index int) *profileEditor {
	values := []string{"", "", "", ""}
	if profile != nil {
		values = []string{profile.Name, profile.User, profile.Password, profile.KeyPath}
	}
	placeholders := []string{"например, root-prod", "root", "необязательно", "~/.ssh/id_ed25519"}
	editor := &profileEditor{editIndex: index}
	if profile != nil {
		editor.original = profile.Name
	}
	for i := range values {
		input := styledTextInput(placeholders[i], values[i], 45)
		editor.inputs = append(editor.inputs, input)
	}
	editor.inputs[2].EchoMode = textinput.EchoPassword
	editor.inputs[2].EchoCharacter = '•'
	editor.setFocus(0)
	return editor
}

func styledTextInput(placeholder, value string, width int) textinput.Model {
	input := textinput.New()
	styles := textinput.DefaultDarkStyles()
	styles.Focused.Text = lipgloss.NewStyle().Foreground(textColor)
	styles.Focused.Placeholder = lipgloss.NewStyle().Foreground(borderMuted).Italic(true)
	styles.Blurred.Text = lipgloss.NewStyle().Foreground(muted)
	styles.Blurred.Placeholder = lipgloss.NewStyle().Foreground(borderMuted)
	styles.Cursor.Color = accentWarm
	input.SetStyles(styles)
	input.Prompt = ""
	input.Placeholder = placeholder
	input.SetValue(value)
	input.SetWidth(width)
	input.CharLimit = 512
	return input
}

func (e *profileEditor) setFocus(focus int) {
	for i := range e.inputs {
		e.inputs[i].Blur()
	}
	e.focus = focus
	if focus >= 0 && focus < len(e.inputs) {
		e.inputs[focus].Focus()
	}
}

func (e *profileEditor) value() config.UserProfile {
	return config.UserProfile{
		Name: strings.TrimSpace(e.inputs[0].Value()), User: strings.TrimSpace(e.inputs[1].Value()),
		Password: e.inputs[2].Value(), KeyPath: strings.TrimSpace(e.inputs[3].Value()),
	}
}

func (m *Model) handleProfileManagerKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	manager := m.profileManager
	if manager == nil {
		return m, nil
	}
	if manager.editor != nil {
		editor := manager.editor
		count := len(editor.inputs) + 2
		switch key.Keystroke() {
		case "esc":
			manager.editor = nil
			manager.status = "Редактирование отменено"
			return m, nil
		case "tab", "down":
			editor.setFocus((editor.focus + 1) % count)
			return m, nil
		case "shift+tab", "up":
			editor.setFocus((editor.focus - 1 + count) % count)
			return m, nil
		case "enter":
			if editor.focus < len(editor.inputs) {
				editor.setFocus(editor.focus + 1)
				return m, nil
			}
			if editor.focus == len(editor.inputs)+1 {
				manager.editor = nil
				return m, nil
			}
			return m, m.saveUserProfile(editor)
		}
		if editor.focus < len(editor.inputs) {
			var cmd tea.Cmd
			editor.inputs[editor.focus], cmd = editor.inputs[editor.focus].Update(key)
			return m, cmd
		}
		return m, nil
	}

	switch key.Keystroke() {
	case "esc":
		m.profileManager = nil
	case "up", "k":
		manager.cursor = max(0, manager.cursor-1)
	case "down", "j":
		manager.cursor = min(max(0, len(m.config.UserProfiles)-1), manager.cursor+1)
	case "n":
		manager.editor = newProfileEditor(nil, -1)
	case "enter", "e":
		if manager.cursor >= 0 && manager.cursor < len(m.config.UserProfiles) {
			profile := m.config.UserProfiles[manager.cursor]
			manager.editor = newProfileEditor(&profile, manager.cursor)
		}
	case "d":
		m.deleteUserProfile(manager.cursor)
	}
	return m, nil
}

func (m *Model) updateProfileManager(message tea.Msg) (tea.Model, tea.Cmd) {
	if m.profileManager == nil || m.profileManager.editor == nil {
		return m, nil
	}
	editor := m.profileManager.editor
	if editor.focus >= 0 && editor.focus < len(editor.inputs) {
		var cmd tea.Cmd
		editor.inputs[editor.focus], cmd = editor.inputs[editor.focus].Update(message)
		return m, cmd
	}
	return m, nil
}

func (m *Model) saveUserProfile(editor *profileEditor) tea.Cmd {
	profile := editor.value()
	if err := profile.Validate(); err != nil {
		m.profileManager.status = "Ошибка: " + err.Error()
		return nil
	}
	for index, existing := range m.config.UserProfiles {
		if index != editor.editIndex && strings.EqualFold(existing.Name, profile.Name) {
			m.profileManager.status = "Профиль с таким названием уже существует"
			return nil
		}
	}
	oldProfiles := append([]config.UserProfile(nil), m.config.UserProfiles...)
	oldSessions := append([]config.Session(nil), m.config.Sessions...)
	if editor.editIndex >= 0 && editor.editIndex < len(m.config.UserProfiles) {
		m.config.UserProfiles[editor.editIndex] = profile
		for i := range m.config.Sessions {
			if strings.EqualFold(m.config.Sessions[i].CredentialProfile, editor.original) {
				m.config.Sessions[i].CredentialProfile = profile.Name
			}
		}
	} else {
		m.config.UserProfiles = append(m.config.UserProfiles, profile)
		m.profileManager.cursor = len(m.config.UserProfiles) - 1
	}
	if err := m.store.Save(m.config); err != nil {
		m.config.UserProfiles, m.config.Sessions = oldProfiles, oldSessions
		m.profileManager.status = "Не удалось сохранить: " + err.Error()
		return nil
	}
	m.profileManager.editor = nil
	m.profileManager.status = "Профиль сохранён"
	return nil
}

func (m *Model) deleteUserProfile(index int) {
	if index < 0 || index >= len(m.config.UserProfiles) {
		return
	}
	name := m.config.UserProfiles[index].Name
	for _, session := range m.config.Sessions {
		if strings.EqualFold(session.CredentialProfile, name) {
			m.profileManager.status = fmt.Sprintf("Профиль используется хостом %q", session.Name)
			return
		}
	}
	old := append([]config.UserProfile(nil), m.config.UserProfiles...)
	m.config.UserProfiles = append(m.config.UserProfiles[:index], m.config.UserProfiles[index+1:]...)
	if err := m.store.Save(m.config); err != nil {
		m.config.UserProfiles = old
		m.profileManager.status = "Не удалось удалить: " + err.Error()
		return
	}
	m.profileManager.cursor = min(index, max(0, len(m.config.UserProfiles)-1))
	m.profileManager.status = "Профиль удалён"
}

func (m *Model) profileManagerView() string {
	manager := m.profileManager
	if manager == nil {
		return m.sessionsView()
	}
	width := min(82, max(38, m.width-6))
	inner := max(20, width-6)
	lines := []string{titleStyle.Render("◆ Профили пользователей"), subtitleStyle.Render("Логин, пароль или приватный ключ"), ""}
	if manager.editor != nil {
		labels := []string{"Название", "Пользователь", "Пароль", "Приватный ключ"}
		for i := range manager.editor.inputs {
			style := lipgloss.NewStyle().Width(18).Foreground(muted)
			if manager.editor.focus == i {
				style = style.Foreground(accentAlt).Bold(true)
			}
			lines = append(lines, style.Render(labels[i])+manager.editor.inputs[i].View())
		}
		lines = append(lines, "")
		for i, label := range []string{"Сохранить", "Отмена"} {
			focused := manager.editor.focus == len(manager.editor.inputs)+i
			lines = append(lines, controlStyle(label, focused))
		}
		lines = append(lines, "", renderHints([2]string{"Tab/↑/↓", "переход"}, [2]string{"Enter", "выбрать"}, [2]string{"Esc", "назад"}))
	} else if len(m.config.UserProfiles) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(muted).Italic(true).Render("Профилей пока нет."))
		lines = append(lines, "", renderHints([2]string{"n", "добавить"}, [2]string{"Esc", "назад"}))
	} else {
		for i, profile := range m.config.UserProfiles {
			auth := "автопоиск ключа"
			if profile.KeyPath != "" {
				auth = profile.KeyPath
			} else if profile.Password != "" {
				auth = "пароль"
			}
			line := truncate(fmt.Sprintf("%s  %s  %s", profile.Name, profile.User, auth), inner)
			style := lipgloss.NewStyle().Width(inner)
			if i == manager.cursor {
				style = selectedStyle.Copy().Width(inner)
			}
			lines = append(lines, style.Render(line))
		}
		lines = append(lines, "", renderHints([2]string{"Enter", "изменить"}, [2]string{"n", "новый"}, [2]string{"d", "удалить"}, [2]string{"Esc", "назад"}))
	}
	if manager.status != "" {
		lines = append(lines, "", statusLine(manager.status))
	}
	box := borderStyle.Copy().BorderForeground(accentWarm).Padding(1, 2).Width(width - 2).Render(strings.Join(lines, "\n"))
	return m.placeCentered(box)
}
