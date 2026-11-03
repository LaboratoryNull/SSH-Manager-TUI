package tui

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

var clickableHintLabels = []string{
	"Ctrl+K", "Ctrl+N", "F1",
	"Shift+Insert/ПКМ", "⇧Ins/ПКМ", "Shift+Insert", "⇧Ins",
	"Ctrl+Shift+Space", "Ctrl+Shift+1…9", "Ctrl+Shift+←/→", "Ctrl+Shift+P", "Ctrl+Shift+C",
	"^⇧Space", "^⇧1…9", "^⇧←/→", "^⇧P",
	"Ctrl+Space", "Завершено", "Alt+1…9", "Alt+←/→", "←/Backspace",
	"Tab/↑/↓", "n/н/Esc", "Ctrl+T", "Ctrl+W", "Ctrl+B", "Ctrl+D",
	"Ctrl+U", "Ctrl+X", "Alt←/→", "Esc q", "n/e/d", "Alt+P", "AltP",
	"p/←/→",
	"^Space", "Enter", "Space", "Backspace", "↑/↓", "←/→", "y/д",
	"shift+tab", "Tab", "Esc", "F5", "^T", "^W", "^B", "^D", "^U", "^⇧C",
	"^X", "d/Del", "Del", "n", "c", "m", "e", "d", "g", "q", "s", "u", "p", "i", "t", "a", "f", "/",
}

func (m *Model) handleMouseClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	mouse := msg.Mouse()
	if mouse.Button == tea.MouseRight {
		if m.page == pageConnected && m.reconnect == nil && m.forwardModal == nil && m.sftpDialog == nil && m.picker == nil && m.hostKeyPrompt == nil {
			if _, ok := m.terminalPointAt(mouse, false); ok {
				m.filesFocused = false
				m.rightPasteDown = true
			}
		}
		return m, nil
	}
	m.rightPasteDown = false
	if mouse.Button != tea.MouseLeft {
		return m, nil
	}
	if mouse.Y == 0 {
		if index := m.clickedTab(mouse.X); index >= 0 {
			m.switchTab(index)
			return m, nil
		}
	}
	// The app bar and the status bar are chrome: a click there activates a
	// hint but must not move focus between the terminal and the files.
	if mouse.Y == 0 || mouse.Y == m.height-1 {
		if label := m.clickedHint(mouse.X, mouse.Y); label != "" {
			return m.activateHint(label)
		}
		return m, nil
	}

	if m.page == pageConnected && m.reconnect == nil && m.forwardModal == nil && m.sftpDialog == nil && m.picker == nil && m.hostKeyPrompt == nil {
		if point, ok := m.terminalPointAt(mouse, false); ok {
			m.filesFocused = false
			m.selection = &terminalSelection{anchor: point, head: point, dragging: true}
			return m, nil
		}
		if mouse.Y > 0 {
			if m.focusSFTPAt(mouse) {
				m.selection = nil
			} else {
				m.filesFocused = false
			}
		}
	}
	if label := m.clickedHint(mouse.X, mouse.Y); label != "" {
		return m.activateHint(label)
	}
	if m.launcherVisible() {
		return m.handleLauncherClick(mouse.Y)
	}
	return m, nil
}

func (m *Model) launcherVisible() bool {
	return m.page == pageSessions && m.palette == nil && m.updateDialog == nil && m.hostKeyPrompt == nil &&
		m.passwordPrompt == nil && m.groupInput == nil && m.profileManager == nil && m.importWizard == nil
}

// handleLauncherClick selects a server with the first click and connects on
// a click on the already highlighted one. The rows start right below the
// thin rule under the column titles.
func (m *Model) handleLauncherClick(y int) (tea.Model, tea.Cmd) {
	if m.connecting || m.confirm {
		return m, nil
	}
	lines := strings.Split(ansi.Strip(m.viewContent()), "\n")
	rule := -1
	for index, line := range lines {
		if strings.Contains(line, strings.Repeat("─", 20)) && strings.Count(line, "│") == 2 {
			rule = index
			break
		}
	}
	if rule < 0 {
		return m, nil
	}
	row := m.launcherRowAt(y - rule - 1)
	if row < 0 {
		return m, nil
	}
	launcher := m.ensureLauncher()
	if launcher.cursor == row {
		return m, m.connectLaunchRow()
	}
	launcher.cursor = row
	m.syncSelection()
	return m, nil
}

func (m *Model) handleMouseMotion(msg tea.MouseMotionMsg) (tea.Model, tea.Cmd) {
	if m.selection == nil || !m.selection.dragging || m.term == nil {
		return m, nil
	}
	point, _ := m.terminalPointAt(msg.Mouse(), true)
	m.selection.head = point
	m.selection.moved = true
	m.selection.text = m.term.SelectionText(m.selection)
	return m, nil
}

func (m *Model) handleMouseWheel(msg tea.MouseWheelMsg) (tea.Model, tea.Cmd) {
	if m.page != pageConnected || m.term == nil || m.reconnect != nil || m.forwardModal != nil || m.sftpDialog != nil || m.picker != nil || m.hostKeyPrompt != nil {
		return m, nil
	}
	mouse := msg.Mouse()
	if _, ok := m.terminalPointAt(mouse, false); !ok {
		return m, nil
	}
	lines := 0
	switch mouse.Button {
	case tea.MouseWheelUp:
		lines = 3
	case tea.MouseWheelDown:
		lines = -3
	default:
		return m, nil
	}
	m.filesFocused = false
	if m.term.ScrollLines(lines) {
		m.selection = nil
	}
	return m, nil
}

func (m *Model) handleMouseRelease(msg tea.MouseReleaseMsg) (tea.Model, tea.Cmd) {
	mouse := msg.Mouse()
	if mouse.Button == tea.MouseRight || m.rightPasteDown {
		m.rightPasteDown = false
		if m.page == pageConnected && m.reconnect == nil && m.forwardModal == nil && m.sftpDialog == nil && m.picker == nil && m.hostKeyPrompt == nil {
			if _, ok := m.terminalPointAt(mouse, false); ok {
				m.filesFocused = false
				return m, m.requestClipboardPaste()
			}
		}
		return m, nil
	}
	if m.selection == nil || !m.selection.dragging || m.term == nil {
		if m.page == pageConnected && m.reconnect == nil && m.forwardModal == nil && m.sftpDialog == nil && m.picker == nil && m.hostKeyPrompt == nil {
			m.focusSFTPAt(msg.Mouse())
		}
		return m, nil
	}
	point, _ := m.terminalPointAt(msg.Mouse(), true)
	if point != m.selection.anchor {
		m.selection.moved = true
	}
	m.selection.head = point
	m.selection.dragging = false
	if !m.selection.hasRange() {
		m.selection = nil
		return m, nil
	}
	m.selection.text = m.term.SelectionText(m.selection)
	if m.selection.text == "" {
		m.status = "Не удалось скопировать пустое выделение"
		return m, nil
	}
	return m, m.beginClipboardCopy(m.selection.text)
}

func (m *Model) focusSFTPAt(mouse tea.Mouse) bool {
	if m.sftpHidden || mouse.Y <= 0 {
		return false
	}
	fileWidth, _, _, _, _ := m.layout()
	if mouse.X < 0 || mouse.X >= fileWidth {
		return false
	}
	m.filesFocused = true
	row := mouse.Y - 3 // tab bar, outer border, then SFTP title
	index := m.fileOffset + row
	if row >= 0 && index >= 0 && index < len(m.visible) {
		m.fileCursor = index
		m.ensureFileVisible()
	}
	return true
}

func (m *Model) terminalPointAt(mouse tea.Mouse, clampOutside bool) (terminalPoint, bool) {
	if m.page != pageConnected || m.term == nil {
		return terminalPoint{}, false
	}
	fileW, _, _, termWidth, termHeight := m.layout()
	originX := 1
	if !m.sftpHidden {
		originX = fileW + 1
	}
	originY := 3 // tab bar, outer border, then SSH title
	x, y := mouse.X-originX, mouse.Y-originY
	inside := x >= 0 && x < termWidth && y >= 0 && y < termHeight
	if !inside && !clampOutside {
		return terminalPoint{}, false
	}
	point := terminalPoint{x: clamp(x, 0, termWidth-1), y: clamp(y, 0, termHeight-1)}
	return m.term.selectionPoint(point), inside
}

func (m *Model) clickedTab(x int) int {
	if len(m.tabs) <= 1 {
		return -1
	}
	used := m.tabStripOffset() - 1
	helpWidth := runewidth.StringWidth(ansi.Strip(m.tabBarHelp()))
	for index := range m.tabs {
		label := " " + strconv.Itoa(index+1) + " " + m.tabTitle(index) + " "
		width := runewidth.StringWidth(label)
		if used+1+width > max(1, m.width-helpWidth-1) {
			break
		}
		if x >= used+1 && x < used+1+width {
			return index
		}
		used += width + 1
	}
	return -1
}

func (m *Model) clickedHint(x, y int) string {
	if y < 0 || y >= m.height {
		return ""
	}
	// In the normal connected view hints only occupy the status bar.
	// Restricting the scan prevents SSH output that happens to say "Enter"
	// or "Esc" from becoming clickable.
	if m.page == pageConnected && m.reconnect == nil && m.forwardModal == nil && m.sftpDialog == nil && m.picker == nil && m.hostKeyPrompt == nil && m.palette == nil && y != 0 && y != m.height-1 {
		return ""
	}
	plain := ansi.Strip(m.viewContent())
	lines := strings.Split(plain, "\n")
	if y >= len(lines) {
		return ""
	}
	line := lines[y]
	for _, label := range clickableHintLabels {
		pattern := " " + label + " "
		for offset := 0; offset < len(line); {
			index := strings.Index(line[offset:], pattern)
			if index < 0 {
				break
			}
			startByte := offset + index
			afterLabel := startByte + len(pattern)
			endByte := len(line)
			if separator := strings.Index(line[afterLabel:], "   "); separator >= 0 {
				endByte = afterLabel + separator
			} else if border := strings.Index(line[afterLabel:], "│"); border >= 0 {
				endByte = afterLabel + border
			}
			startX := runewidth.StringWidth(line[:startByte])
			endX := runewidth.StringWidth(line[:endByte])
			if x >= startX && x < endX {
				return label
			}
			offset = afterLabel
		}
	}
	// hintsView uses this unstyled compact fallback in extremely narrow areas.
	if index := strings.Index(line, "Esc назад"); index >= 0 {
		startX := runewidth.StringWidth(line[:index])
		if x >= startX && x < startX+runewidth.StringWidth("Esc назад") {
			return "Esc"
		}
	}
	return ""
}

func (m *Model) activateHint(label string) (tea.Model, tea.Cmd) {
	key := func(code rune, mod tea.KeyMod, text string) tea.KeyPressMsg {
		return tea.KeyPressMsg(tea.Key{Code: code, Mod: mod, Text: text})
	}
	keys := currentPlatformKeys()
	compact := compactPlatformKeys()
	primaryMod := platformPrimaryMod()
	var press tea.KeyPressMsg
	platformHint := true
	switch {
	case isPlatformHint(label, keys.newTab, compact.newTab):
		press = key('t', primaryMod, "")
	case isPlatformHint(label, keys.closeTab, compact.closeTab):
		press = key('w', primaryMod, "")
	case isPlatformHint(label, keys.toggleSFTP, compact.toggleSFTP):
		press = key('b', primaryMod, "")
	case isPlatformHint(label, keys.download, compact.download):
		press = key('d', primaryMod, "")
	case isPlatformHint(label, keys.upload, compact.upload):
		press = key('u', primaryMod, "")
	case isPlatformHint(label, keys.cancel, compact.cancel):
		press = key('x', primaryMod, "")
	case isPlatformHint(label, keys.focus, compact.focus):
		press = key(tea.KeySpace, platformFocusMod(), "")
	case isPlatformHint(label, keys.copy, compact.copy):
		press = key('c', platformCopyMod(), "")
	case isPlatformHint(label, keys.ports, compact.ports):
		press = key('p', platformNavigationMod(), "")
	case isPlatformHint(label, keys.tabs, compact.tabs, keys.numberedTabs):
		if len(m.tabs) > 1 {
			m.switchTab((m.activeTab + 1) % len(m.tabs))
		}
		return m, nil
	default:
		platformHint = false
	}
	if !platformHint {
		switch label {
		case "Ctrl+K", "F1":
			m.openCommandPalette()
			return m, nil
		case "Ctrl+N":
			if m.page == pageSessions {
				m.openNewSessionForm()
			}
			return m, nil
		case "Shift+Insert/ПКМ", "⇧Ins/ПКМ", "Shift+Insert", "⇧Ins":
			press = key(tea.KeyInsert, tea.ModShift, "")
		case "Esc q":
			if m.page == pageConnected {
				m.disconnect("Отключено")
			}
			return m, nil
		case "Завершено":
			m.finishHistoryImport()
			return m, nil
		case "Enter":
			press = key(tea.KeyEnter, 0, "")
		case "Space":
			press = key(tea.KeySpace, 0, " ")
		case "Tab", "Tab/↑/↓":
			press = key(tea.KeyTab, 0, "")
		case "shift+tab":
			press = key(tea.KeyTab, tea.ModShift, "")
		case "Esc", "n/н/Esc":
			press = key(tea.KeyEsc, 0, "")
		case "Backspace", "←/Backspace":
			press = key(tea.KeyBackspace, 0, "")
		case "↑/↓":
			press = key(tea.KeyDown, 0, "")
		case "←/→":
			press = key(tea.KeyRight, 0, "")
		case "p/←/→":
			press = key('p', 0, "p")
		case "F5":
			press = key(tea.KeyF5, 0, "")
		case "Del", "d/Del":
			press = key(tea.KeyDelete, 0, "")
		case "y/д":
			press = key('y', 0, "y")
		case "n/e/d":
			press = key('n', 0, "n")
		case "n", "c", "m", "e", "d", "g", "q", "s", "u", "p", "i", "t", "a", "f", "/":
			r := []rune(label)[0]
			press = key(r, 0, string(r))
		default:
			return m, nil
		}
	}
	if model, cmd, handled := m.handleTabKey(press); handled {
		return model, cmd
	}
	return m.handleKey(press)
}
