package tui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	appupdate "ssh-manager-tui/internal/update"
)

type updatePhase int

const (
	updateAvailable updatePhase = iota
	updateInstalling
	updateInstalled
	updateFailed
)

type updateDialog struct {
	currentVersion string
	latestVersion  string
	phase          updatePhase
	selected       int
	status         string
}

type updateCheckMsg struct {
	result appupdate.Result
	err    error
}

type updateInstallMsg struct {
	result appupdate.Result
	err    error
}

type elevatedUpdateFinishedMsg struct{ err error }

func (m *Model) checkForUpdate() tea.Cmd {
	if m.updateManifestURL == "" || !strings.HasPrefix(m.appVersion, "v") {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		result, err := appupdate.Run(ctx, appupdate.Options{
			CurrentVersion: m.appVersion,
			ManifestURL:    m.updateManifestURL,
			Token:          m.updateToken,
			CheckOnly:      true,
		})
		return updateCheckMsg{result: result, err: err}
	}
}

func (m *Model) handleUpdateCheck(message updateCheckMsg) (tea.Model, tea.Cmd) {
	if message.err != nil || !message.result.Available || m.updateDialog != nil {
		return m, nil
	}
	m.updateDialog = &updateDialog{
		currentVersion: message.result.CurrentVersion,
		latestVersion:  message.result.LatestVersion,
		phase:          updateAvailable,
	}
	return m, nil
}

func (m *Model) installUpdate() tea.Cmd {
	if m.updateDialog == nil {
		return nil
	}
	m.updateDialog.phase = updateInstalling
	m.updateDialog.status = "Скачиваем и проверяем обновление…"
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		result, err := appupdate.Run(ctx, appupdate.Options{
			CurrentVersion: m.appVersion,
			ManifestURL:    m.updateManifestURL,
			Token:          m.updateToken,
		})
		return updateInstallMsg{result: result, err: err}
	}
}

func (m *Model) handleUpdateInstall(message updateInstallMsg) (tea.Model, tea.Cmd) {
	if m.updateDialog == nil {
		return m, nil
	}
	if message.err != nil {
		if runtime.GOOS != "windows" && isPermissionError(message.err) {
			m.updateDialog.status = "Для обновления системного файла нужны права администратора…"
			return m, m.elevatedUpdateCommand()
		}
		m.updateDialog.phase = updateFailed
		m.updateDialog.status = "Не удалось обновиться: " + message.err.Error()
		return m, nil
	}
	if message.result.Deferred {
		m.close()
		return m, tea.Quit
	}
	m.updateDialog.phase = updateInstalled
	m.updateDialog.status = "Обновление установлено. Перезапустите sshm, чтобы применить новую версию."
	return m, nil
}

func isPermissionError(err error) bool {
	return errors.Is(err, os.ErrPermission) || strings.Contains(strings.ToLower(err.Error()), "permission denied")
}

func (m *Model) elevatedUpdateCommand() tea.Cmd {
	executable, executableErr := os.Executable()
	sudo, sudoErr := exec.LookPath("sudo")
	if executableErr != nil || sudoErr != nil {
		return func() tea.Msg {
			if executableErr != nil {
				return elevatedUpdateFinishedMsg{err: executableErr}
			}
			return elevatedUpdateFinishedMsg{err: sudoErr}
		}
	}
	command := exec.Command(sudo, "--", executable, "update")
	return tea.ExecProcess(command, func(err error) tea.Msg {
		return elevatedUpdateFinishedMsg{err: err}
	})
}

func (m *Model) handleElevatedUpdateFinished(message elevatedUpdateFinishedMsg) (tea.Model, tea.Cmd) {
	if m.updateDialog == nil {
		return m, nil
	}
	if message.err != nil {
		m.updateDialog.phase = updateFailed
		m.updateDialog.status = "Не удалось обновиться с правами администратора: " + message.err.Error()
		return m, nil
	}
	m.updateDialog.phase = updateInstalled
	m.updateDialog.status = "Обновление установлено. Перезапустите sshm, чтобы применить новую версию."
	return m, nil
}

func (m *Model) handleUpdateDialogKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if matchesPlatformShortcut(key, 'q', false) {
		m.close()
		return m, tea.Quit
	}
	dialog := m.updateDialog
	if dialog == nil {
		return m, nil
	}
	if dialog.phase == updateInstalling {
		return m, nil
	}
	if dialog.phase == updateInstalled {
		if key.String() == "enter" || key.String() == "esc" {
			m.updateDialog = nil
		}
		return m, nil
	}
	switch key.String() {
	case "left", "right", "tab", "shift+tab":
		dialog.selected = 1 - dialog.selected
	case "u", "г":
		dialog.selected = 0
		return m, m.installUpdate()
	case "esc", "l", "д":
		m.updateDialog = nil
	case "enter":
		if dialog.selected == 0 {
			return m, m.installUpdate()
		}
		m.updateDialog = nil
	}
	return m, nil
}

func (m *Model) updateDialogView() string {
	dialog := m.updateDialog
	if dialog == nil {
		return ""
	}
	boxWidth := min(76, max(30, m.width-6))
	innerWidth := max(20, boxWidth-6)
	heading := "ДОСТУПНО ОБНОВЛЕНИЕ"
	description := "Новая версия SSH Manager TUI готова к установке."
	if dialog.phase == updateInstalling {
		heading = "ОБНОВЛЕНИЕ SSH MANAGER"
		description = dialog.status
	} else if dialog.phase == updateInstalled {
		heading = "ОБНОВЛЕНИЕ УСТАНОВЛЕНО"
		description = dialog.status
	} else if dialog.phase == updateFailed {
		heading = "ОШИБКА ОБНОВЛЕНИЯ"
		description = dialog.status
	}
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(warningColor).Render("◆ " + heading),
		"",
		lipgloss.NewStyle().Foreground(textColor).Width(innerWidth).Render(description),
		"",
		lipgloss.NewStyle().Foreground(muted).Render("Установлена") + "  " + lipgloss.NewStyle().Foreground(textColor).Render(dialog.currentVersion),
		lipgloss.NewStyle().Foreground(muted).Render("Новая версия") + "  " + lipgloss.NewStyle().Bold(true).Foreground(successColor).Render(dialog.latestVersion),
	}
	switch dialog.phase {
	case updateAvailable, updateFailed:
		lines = append(lines, "", m.updateDialogButtons())
	case updateInstalling:
		lines = append(lines, "", lipgloss.NewStyle().Bold(true).Foreground(accentAlt).Render("◐ Пожалуйста, подождите"))
	case updateInstalled:
		lines = append(lines, "", selectedStyle.Copy().Padding(0, 2).Render("Понятно"))
	}
	box := borderStyle.Copy().BorderForeground(warningColor).Padding(1, 2).Width(boxWidth - 2).Render(strings.Join(lines, "\n"))
	return m.placeCentered(box)
}

func (m *Model) updateDialogButtons() string {
	labels := []string{"Обновить", "Позже"}
	buttons := make([]string, len(labels))
	for index, label := range labels {
		style := lipgloss.NewStyle().Foreground(muted).Padding(0, 2)
		if index == m.updateDialog.selected {
			style = selectedStyle.Copy().Padding(0, 2)
		}
		buttons[index] = style.Render(label)
	}
	return strings.Join(buttons, "   ")
}

func (m *Model) handleUpdateDialogClick(message tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	mouse := message.Mouse()
	if mouse.Button != tea.MouseLeft || m.updateDialog == nil || m.updateDialog.phase == updateInstalling {
		return m, nil
	}
	labels := []string{"Обновить", "Позже"}
	if m.updateDialog.phase == updateInstalled {
		labels = []string{"Понятно"}
	}
	plain := ansi.Strip(m.viewContent())
	lines := strings.Split(plain, "\n")
	if mouse.Y < 0 || mouse.Y >= len(lines) {
		return m, nil
	}
	for index, label := range labels {
		start := strings.Index(lines[mouse.Y], label)
		if start < 0 {
			continue
		}
		startX := runewidth.StringWidth(lines[mouse.Y][:start])
		if mouse.X < startX || mouse.X >= startX+runewidth.StringWidth(label) {
			continue
		}
		if label == "Понятно" || index == 1 {
			m.updateDialog = nil
			return m, nil
		}
		m.updateDialog.selected = 0
		return m, m.installUpdate()
	}
	return m, nil
}
