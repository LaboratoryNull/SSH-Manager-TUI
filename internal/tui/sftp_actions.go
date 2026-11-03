package tui

import (
	"fmt"
	"path"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/pkg/sftp"
)

type sftpAction uint8

const (
	sftpActionDelete sftpAction = iota + 1
	sftpActionMkdir
	sftpActionCopy
	sftpActionMove
)

type sftpActionDialog struct {
	action sftpAction
	source *fileNode
	parent *fileNode
	input  textinput.Model
	status string
}

type sftpActionDoneMsg struct {
	tabID        uint64
	operationID  uint64
	action       sftpAction
	source       string
	target       string
	refreshPaths []string
	err          error
}

func sftpActionLabel(action sftpAction) string {
	switch action {
	case sftpActionDelete:
		return "Удаление"
	case sftpActionMkdir:
		return "Создание каталога"
	case sftpActionCopy:
		return "Копирование"
	case sftpActionMove:
		return "Перемещение"
	default:
		return "SFTP-операция"
	}
}

func (m *Model) openSFTPAction(action sftpAction) tea.Cmd {
	if m.sftpBusy {
		m.status = "SFTP: дождитесь завершения текущей операции"
		return nil
	}
	if m.transferring {
		m.status = "SFTP: дождитесь завершения передачи файлов"
		return nil
	}
	if m.conn == nil || len(m.visible) == 0 || m.fileCursor < 0 || m.fileCursor >= len(m.visible) {
		return nil
	}
	selected := m.visible[m.fileCursor]
	parent := findParentFileNode(m.root, selected)
	if action == sftpActionMkdir {
		if selected.Dir {
			parent = selected
		}
		if parent == nil {
			m.status = "SFTP: не удалось определить каталог для создания"
			return nil
		}
	} else if selected == m.root {
		m.status = "SFTP: корень текущего дерева нельзя удалить, копировать или перемещать"
		return nil
	}

	dialog := &sftpActionDialog{action: action, source: selected, parent: parent}
	if action != sftpActionDelete {
		placeholder, value := "Имя нового каталога", ""
		if action == sftpActionCopy || action == sftpActionMove {
			placeholder = "/путь/каталога/назначения"
			if parent != nil {
				value = parent.Path
			}
		}
		dialog.input = styledTextInput(placeholder, value, min(68, max(18, m.width-18)))
		dialog.input.Focus()
	}
	m.sftpDialog = dialog
	return textinput.Blink
}

func (m *Model) handleSFTPActionKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	dialog := m.sftpDialog
	if dialog == nil {
		return m, nil
	}
	switch key.Keystroke() {
	case "esc":
		m.sftpDialog = nil
		m.status = "SFTP: операция отменена"
		return m, nil
	case "n", "н":
		if dialog.action == sftpActionDelete {
			m.sftpDialog = nil
			m.status = "SFTP: удаление отменено"
			return m, nil
		}
	case "enter", "y", "д":
		if dialog.action == sftpActionDelete || key.Keystroke() == "enter" {
			return m, m.executeSFTPAction()
		}
	}
	if dialog.action != sftpActionDelete {
		var cmd tea.Cmd
		dialog.input, cmd = dialog.input.Update(key)
		dialog.status = ""
		return m, cmd
	}
	return m, nil
}

func (m *Model) executeSFTPAction() tea.Cmd {
	dialog := m.sftpDialog
	if dialog == nil || m.conn == nil || dialog.source == nil {
		return nil
	}
	source := path.Clean(dialog.source.Path)
	target := ""
	refreshPaths := []string{path.Dir(source)}
	switch dialog.action {
	case sftpActionDelete:
		if source == "/" {
			dialog.status = "Корень файловой системы удалять нельзя"
			return nil
		}
	case sftpActionMkdir:
		name, err := safeChildName(strings.TrimSpace(dialog.input.Value()))
		if err != nil {
			dialog.status = "Укажите имя без / и переходов . или .."
			return nil
		}
		target = path.Join(dialog.parent.Path, name)
		refreshPaths = []string{dialog.parent.Path}
	case sftpActionCopy, sftpActionMove:
		destination := path.Clean(strings.TrimSpace(dialog.input.Value()))
		if !path.IsAbs(destination) {
			dialog.status = "Укажите абсолютный путь каталога назначения"
			return nil
		}
		var err error
		target, err = sftpDestinationPath(source, destination, dialog.source.Dir)
		if err != nil {
			dialog.status = err.Error()
			return nil
		}
		refreshPaths = append(refreshPaths, destination)
	default:
		return nil
	}

	m.nextSFTPOperationID++
	m.sftpOperationID = m.nextSFTPOperationID
	m.sftpBusy = true
	m.status = fmt.Sprintf("SFTP: %s…", strings.ToLower(sftpActionLabel(dialog.action)))
	action, tabID, operationID, client := dialog.action, m.tabID, m.sftpOperationID, m.conn.SFTP
	m.sftpDialog = nil
	return runSFTPAction(tabID, operationID, client, action, source, target, refreshPaths)
}

func sftpDestinationPath(source, destinationDirectory string, sourceIsDir bool) (string, error) {
	source = path.Clean(source)
	destinationDirectory = path.Clean(destinationDirectory)
	if !path.IsAbs(source) || !path.IsAbs(destinationDirectory) {
		return "", fmt.Errorf("пути должны быть абсолютными")
	}
	if source == "/" {
		return "", fmt.Errorf("корень файловой системы нельзя копировать или перемещать")
	}
	if sourceIsDir && pathContains(source, destinationDirectory) {
		return "", fmt.Errorf("каталог назначения не может находиться внутри исходного")
	}
	target := path.Join(destinationDirectory, path.Base(source))
	if target == source {
		return "", fmt.Errorf("выберите другой каталог назначения")
	}
	return target, nil
}

func pathContains(parent, candidate string) bool {
	parent, candidate = path.Clean(parent), path.Clean(candidate)
	if parent == "/" {
		return strings.HasPrefix(candidate, "/")
	}
	return candidate == parent || strings.HasPrefix(candidate, parent+"/")
}

func runSFTPAction(tabID, operationID uint64, client *sftp.Client, action sftpAction, source, target string, refreshPaths []string) tea.Cmd {
	return func() tea.Msg {
		var err error
		if client == nil {
			err = fmt.Errorf("SFTP connection is closed")
			return sftpActionDoneMsg{
				tabID: tabID, operationID: operationID, action: action,
				source: source, target: target, refreshPaths: refreshPaths, err: err,
			}
		}
		switch action {
		case sftpActionDelete:
			err = removeRemoteTree(client, source)
		case sftpActionMkdir:
			if err = ensureRemotePathAvailable(client, target); err == nil {
				err = client.Mkdir(target)
			}
		case sftpActionCopy, sftpActionMove:
			if err = ensureRemoteDirectory(client, path.Dir(target)); err != nil {
				break
			}
			if err = ensureRemotePathAvailable(client, target); err != nil {
				break
			}
			if action == sftpActionCopy {
				err = copyRemoteWithin(client, source, target)
			} else {
				err = moveRemoteWithin(client, source, target)
			}
		}
		return sftpActionDoneMsg{
			tabID: tabID, operationID: operationID, action: action,
			source: source, target: target, refreshPaths: refreshPaths, err: err,
		}
	}
}

func (m *Model) handleSFTPActionDone(msg sftpActionDoneMsg) (tea.Model, tea.Cmd) {
	if msg.operationID != m.sftpOperationID {
		return m, nil
	}
	m.sftpBusy = false
	if msg.err != nil {
		m.status = fmt.Sprintf("Ошибка SFTP (%s): %v", strings.ToLower(sftpActionLabel(msg.action)), msg.err)
		return m, m.reloadSFTPPaths(msg.refreshPaths)
	}
	switch msg.action {
	case sftpActionDelete:
		m.status = "SFTP: удалено " + msg.source
	case sftpActionMkdir:
		m.status = "SFTP: создан каталог " + msg.target
	case sftpActionCopy:
		m.status = "SFTP: скопировано в " + msg.target
	case sftpActionMove:
		m.status = "SFTP: перемещено в " + msg.target
	}
	return m, m.reloadSFTPPaths(msg.refreshPaths)
}

func (m *Model) reloadSFTPPaths(paths []string) tea.Cmd {
	if m.conn == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(paths))
	var commands []tea.Cmd
	for _, directory := range paths {
		directory = path.Clean(directory)
		if _, exists := seen[directory]; exists {
			continue
		}
		seen[directory] = struct{}{}
		node := findFileNode(m.root, directory)
		if node == nil || !node.Dir {
			continue
		}
		node.Loaded, node.Loading = false, true
		commands = append(commands, loadDir(m.conn.SFTP, node))
	}
	return tea.Batch(commands...)
}

func (m *Model) sftpActionView() string {
	dialog := m.sftpDialog
	if dialog == nil {
		return m.connectedView()
	}
	boxWidth := min(86, max(38, m.width-6))
	innerWidth := max(20, boxWidth-6)
	lines := []string{titleStyle.Render(sftpActionLabel(dialog.action)), ""}
	if dialog.action == sftpActionMkdir {
		lines = append(lines,
			lipgloss.NewStyle().Foreground(muted).Render("Родительский каталог"),
			truncate(dialog.parent.Path, innerWidth), "",
			lipgloss.NewStyle().Foreground(muted).Render("Имя нового каталога"),
			dialog.input.View(),
		)
	} else {
		lines = append(lines,
			lipgloss.NewStyle().Foreground(muted).Render("Выбранный объект"),
			truncate(dialog.source.Path, innerWidth),
		)
		if dialog.action == sftpActionDelete {
			warning := "Файл будет удалён без возможности восстановления."
			if dialog.source.Dir {
				warning = "Каталог и всё его содержимое будут удалены без возможности восстановления."
			}
			lines = append(lines, "", lipgloss.NewStyle().Foreground(errorColor).Render(truncate("⚠ "+warning, innerWidth)))
		} else {
			lines = append(lines, "",
				lipgloss.NewStyle().Foreground(muted).Render("Каталог назначения"),
				dialog.input.View(),
				lipgloss.NewStyle().Foreground(borderMuted).Render(truncate("Имя объекта сохранится: "+path.Base(dialog.source.Path), innerWidth)),
			)
		}
	}
	lines = append(lines, "")
	if dialog.status != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(errorColor).Render(truncate(dialog.status, innerWidth)))
	} else {
		lines = append(lines, "")
	}
	help := hintsView(innerWidth,
		[][2]string{{"Enter", "подтвердить"}, {"Esc", "отмена"}},
		[][2]string{{"Enter", "да"}, {"Esc", "нет"}},
	)
	lines = append(lines, help)
	box := borderStyle.Copy().BorderForeground(accentWarm).Padding(1, 2).Width(boxWidth - 2).Render(strings.Join(lines, "\n"))
	return m.placeCentered(box)
}
