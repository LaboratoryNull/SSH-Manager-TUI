package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"ssh-manager-tui/internal/config"
	"ssh-manager-tui/internal/remote"
)

type hostKeyPrompt struct {
	change  *remote.HostKeyMismatchError
	profile config.Session
	keyTest bool
	status  string
}

func (m *Model) handleHostKeyPrompt(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	prompt := m.hostKeyPrompt
	if prompt == nil {
		return m, nil
	}
	switch key.Keystroke() {
	case "y", "д":
		if err := remote.AcceptHostKey(prompt.change); err != nil {
			prompt.status = "Не удалось сохранить ключ: " + err.Error()
			return m, nil
		}
		profile, keyTest := prompt.profile, prompt.keyTest
		m.hostKeyPrompt = nil
		if keyTest {
			m.testing = true
			m.status = "Ключ сервера принят, повторная проверка авторизации…"
			return m, testKeyCommand(m.tabID, profile)
		}
		m.connecting = true
		m.status = "Ключ сервера принят, повторное подключение…"
		width, height := m.consoleSize()
		return m, connectCommand(m.tabID, profile, width, height)
	case "n", "н", "esc":
		m.hostKeyPrompt = nil
		m.status = "Подключение отменено: новый ключ сервера не принят"
		return m, nil
	}
	return m, nil
}

func (m *Model) hostKeyPromptView() string {
	prompt := m.hostKeyPrompt
	if prompt == nil || prompt.change == nil {
		return m.sessionsView()
	}
	boxWidth := min(96, max(50, m.width-6))
	innerWidth := max(20, boxWidth-6)
	change := prompt.change
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(errorColor).Render("⚠ Ключ SSH-сервера изменился"),
		"",
		lipgloss.NewStyle().Foreground(warningColor).Render("Сохранённый ключ этого хоста не совпадает с полученным."),
		lipgloss.NewStyle().Foreground(muted).Render("Это бывает после переустановки сервера, но также может означать MITM-атаку."),
		"",
		fieldLine("Хост", change.Hostname, innerWidth),
		fieldLine("Адрес", change.Address, innerWidth),
		fieldLine("Алгоритм", change.Algorithm, innerWidth),
		fieldLine("Новый SHA256", change.Fingerprint, innerWidth),
		"",
		lipgloss.NewStyle().Bold(true).Foreground(muted).Render("Ранее сохранённые ключи:"),
	}
	if len(change.Expected) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(muted).Italic(true).Render("нет данных"))
	} else {
		for _, expected := range change.Expected {
			source := expected.Filename
			if expected.Line > 0 {
				source += fmt.Sprintf(":%d", expected.Line)
			}
			oldKey := fmt.Sprintf("• %s  %s", expected.Algorithm, expected.Fingerprint)
			lines = append(lines,
				lipgloss.NewStyle().Foreground(muted).Render(truncate(oldKey, innerWidth)),
				lipgloss.NewStyle().Foreground(borderMuted).Render(truncate("  "+source, innerWidth)),
			)
		}
	}
	lines = append(lines, "")
	if prompt.status != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(errorColor).Render(truncate(prompt.status, innerWidth)), "")
	}
	lines = append(lines, hintsView(innerWidth,
		[][2]string{{"y/д", "принять новый ключ"}, {"n/н/Esc", "отменить подключение"}},
		[][2]string{{"y", "принять"}, {"Esc", "отменить"}},
	))
	box := borderStyle.Copy().BorderForeground(errorColor).Padding(1, 2).Width(boxWidth - 2).
		Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, m.contentHeight(), lipgloss.Center, lipgloss.Center, box)
}

func fieldLine(label, value string, width int) string {
	labelView := lipgloss.NewStyle().Width(16).Foreground(muted).Render(label)
	return labelView + lipgloss.NewStyle().Foreground(textColor).Render(truncate(value, max(1, width-16)))
}
