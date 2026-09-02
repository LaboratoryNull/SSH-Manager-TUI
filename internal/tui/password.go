package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"ssh-manager-tui/internal/config"
)

const passwordAttemptLimit = 3

type passwordPrompt struct {
	input    textinput.Model
	profile  config.Session
	failures int
	status   string
}

func newPasswordPrompt(profile config.Session) *passwordPrompt {
	input := styledTextInput("Введите новый пароль", "", 42)
	input.EchoMode = textinput.EchoPassword
	input.EchoCharacter = '•'
	input.Focus()
	return &passwordPrompt{input: input, profile: profile}
}

func (m *Model) handleAuthenticationFailure(profile config.Session) (tea.Model, tea.Cmd) {
	if m.passwordPrompt == nil {
		m.passwordPrompt = newPasswordPrompt(profile)
		m.status = ""
		return m, textinput.Blink
	}

	prompt := m.passwordPrompt
	prompt.failures++
	if prompt.failures >= passwordAttemptLimit {
		prompt.input.Blur()
		m.passwordPrompt = nil
		m.status = "Ошибка: пароль не подошёл после 3 попыток"
		return m, nil
	}
	prompt.profile = profile
	prompt.input.SetValue("")
	prompt.input.Focus()
	remaining := passwordAttemptLimit - prompt.failures
	prompt.status = fmt.Sprintf("Пароль не подошёл. Осталось попыток: %d", remaining)
	return m, textinput.Blink
}

func (m *Model) handlePasswordPrompt(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	prompt := m.passwordPrompt
	if prompt == nil {
		return m, nil
	}
	if m.connecting {
		return m, nil
	}
	switch key.Keystroke() {
	case "esc":
		prompt.input.Blur()
		m.passwordPrompt = nil
		m.status = "Ввод нового пароля отменён"
		return m, nil
	case "enter":
		password := prompt.input.Value()
		if password == "" {
			prompt.status = "Введите пароль"
			return m, nil
		}
		profile := prompt.profile
		profile.Password = password
		prompt.profile = profile
		prompt.status = fmt.Sprintf("Проверка пароля, попытка %d из %d…", prompt.failures+1, passwordAttemptLimit)
		m.connecting = true
		width, height := m.consoleSize()
		return m, passwordConnectCommand(m.tabID, profile, width, height)
	}
	var cmd tea.Cmd
	prompt.input, cmd = prompt.input.Update(key)
	prompt.status = ""
	return m, cmd
}

func (m *Model) saveConnectedPassword(profile config.Session) error {
	if profile.CredentialProfile != "" {
		for index := range m.config.UserProfiles {
			if !strings.EqualFold(m.config.UserProfiles[index].Name, profile.CredentialProfile) {
				continue
			}
			oldPassword := m.config.UserProfiles[index].Password
			m.config.UserProfiles[index].Password = profile.Password
			if m.store == nil {
				return nil
			}
			if err := m.store.Save(m.config); err != nil {
				m.config.UserProfiles[index].Password = oldPassword
				return err
			}
			return nil
		}
		return fmt.Errorf("профиль учётных данных %q не найден", profile.CredentialProfile)
	}

	for index := range m.config.Sessions {
		if m.config.Sessions[index].Name != profile.Name {
			continue
		}
		oldPassword := m.config.Sessions[index].Password
		m.config.Sessions[index].Password = profile.Password
		if m.store == nil {
			return nil
		}
		if err := m.store.Save(m.config); err != nil {
			m.config.Sessions[index].Password = oldPassword
			return err
		}
		return nil
	}
	return fmt.Errorf("хост %q не найден в конфигурации", profile.Name)
}

func (m *Model) passwordPromptView() string {
	prompt := m.passwordPrompt
	if prompt == nil {
		return m.sessionsView()
	}
	boxWidth := min(70, max(44, m.width-6))
	innerWidth := max(18, boxWidth-6)
	credentialTarget := "Пароль будет сохранён для этого хоста."
	if prompt.profile.CredentialProfile != "" {
		credentialTarget = "Пароль будет сохранён в профиле «" + prompt.profile.CredentialProfile + "»."
	}
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(warningColor).Render("⚠ Требуется новый пароль"),
		"",
		fieldLine("Хост", prompt.profile.User+"@"+prompt.profile.Host, innerWidth),
		lipgloss.NewStyle().Foreground(muted).Render(fmt.Sprintf("Попытка %d из %d", prompt.failures+1, passwordAttemptLimit)),
		"",
		lipgloss.NewStyle().Foreground(accentAlt).Bold(true).Render("Пароль"),
		prompt.input.View(),
		"",
		lipgloss.NewStyle().Foreground(muted).Render(truncate(credentialTarget, innerWidth)),
	}
	if prompt.status != "" {
		color := errorColor
		if m.connecting {
			color = accentAlt
		}
		lines = append(lines, "", lipgloss.NewStyle().Foreground(color).Render(truncate(prompt.status, innerWidth)))
	}
	lines = append(lines, "", hintsView(innerWidth,
		[][2]string{{"Enter", "проверить пароль"}, {"Esc", "отмена"}},
	))
	box := borderStyle.Copy().BorderForeground(warningColor).Padding(1, 2).Width(boxWidth - 2).
		Render(strings.Join(lines, "\n"))
	return m.placeCentered(box)
}
