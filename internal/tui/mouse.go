package tui

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

var clickableHintLabels = []string{
	"⌘Space", "⌘T", "⌘W", "⌘B", "⌘C", "⌘V", "⌘D", "⌘U", "⌘X", "⌘Q",
	"⌥1…9", "⌥←/→", "⌥P",
	"Ctrl+Shift+C", "Ctrl+Shift+V", "Ctrl+Space", "Завершено", "Alt+1…9", "Alt+←/→", "←/Backspace",
	"Tab/↑/↓", "n/н/Esc", "Ctrl+T", "Ctrl+W", "Ctrl+B", "Ctrl+D",
	"Ctrl+U", "Ctrl+X", "Alt←/→", "Esc q", "n/e/d", "Alt+P", "AltP",
	"p/←/→",
	"^Space", "Enter", "Space", "Backspace", "↑/↓", "←/→", "y/д",
	"shift+tab", "Tab", "Esc", "F5", "^T", "^W", "^B", "^D", "^U", "^⇧C", "^⇧V",
	"^X", "n", "e", "d", "g", "q", "s", "u", "p", "i", "a", "f", "/",
}

func (m *Model) handleMouseClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	mouse := msg.Mouse()
	if mouse.Button != tea.MouseLeft {
		return m, nil
	}
	if mouse.Y == 0 {
		if index := m.clickedTab(mouse.X); index >= 0 {
			m.switchTab(index)
			return m, nil
		}
	}

	if m.page == pageConnected && m.forwardModal == nil && m.picker == nil && m.hostKeyPrompt == nil {
		if point, ok := m.terminalPointAt(mouse, false); ok {
			m.filesFocused = false
			m.selection = &terminalSelection{anchor: point, head: point, dragging: true}
			return m, nil
		}
		fileW, _, _, _, _ := m.layout()
		if mouse.Y > 0 {
			if !m.sftpHidden && mouse.X < fileW {
				m.filesFocused = true
				m.selection = nil
			} else {
				m.filesFocused = false
			}
		}
	}
	if label := m.clickedHint(mouse.X, mouse.Y); label != "" {
		return m.activateHint(label)
	}
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

func (m *Model) handleMouseRelease(msg tea.MouseReleaseMsg) (tea.Model, tea.Cmd) {
	if m.selection == nil || !m.selection.dragging || m.term == nil {
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
	m.status = "Текст выделен — используйте сочетание копирования"
	return m, nil
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
	used := 0
	keys := compactPlatformKeys()
	helpWidth := runewidth.StringWidth(ansi.Strip(renderHints(
		[2]string{keys.newTab, "+"}, [2]string{keys.closeTab, "×"}, [2]string{keys.tabs, "вкладки"},
	)))
	for index := range m.tabs {
		label := " " + strconv.Itoa(index+1) + " " + m.tabTitle(index) + " "
		width := runewidth.StringWidth(label)
		if used+1+width > max(1, m.width-helpWidth-1) {
			break
		}
		if x >= used && x < used+width {
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
	// In the normal connected view hints only occupy the tab bar and the last
	// inner row. Restricting the scan prevents SSH output that happens to say
	// "Enter" or "Esc" from becoming clickable.
	if m.page == pageConnected && m.forwardModal == nil && m.picker == nil && m.hostKeyPrompt == nil && y != 0 && y != m.height-2 {
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
	commandMod := platformCommandMod()
	var press tea.KeyPressMsg
	platformHint := true
	switch {
	case isPlatformHint(label, keys.newTab, compact.newTab):
		press = key('t', commandMod, "")
	case isPlatformHint(label, keys.closeTab, compact.closeTab):
		press = key('w', commandMod, "")
	case isPlatformHint(label, keys.toggleSFTP, compact.toggleSFTP):
		press = key('b', commandMod, "")
	case isPlatformHint(label, keys.download, compact.download):
		press = key('d', commandMod, "")
	case isPlatformHint(label, keys.upload, compact.upload):
		press = key('u', commandMod, "")
	case isPlatformHint(label, keys.cancel, compact.cancel):
		press = key('x', commandMod, "")
	case isPlatformHint(label, keys.focus, compact.focus):
		press = key(tea.KeySpace, commandMod, "")
	case isPlatformHint(label, keys.copy, compact.copy):
		mod := commandMod
		if keys.copy == "Ctrl+Shift+C" {
			mod |= tea.ModShift
		}
		press = key('c', mod, "")
	case isPlatformHint(label, keys.paste, compact.paste):
		mod := commandMod
		if keys.paste == "Ctrl+Shift+V" {
			mod |= tea.ModShift
		}
		press = key('v', mod, "")
	case isPlatformHint(label, keys.ports, compact.ports):
		press = key('p', tea.ModAlt, "")
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
		case "y/д":
			press = key('y', 0, "y")
		case "n/e/d":
			press = key('n', 0, "n")
		case "n", "e", "d", "g", "q", "s", "u", "p", "i", "a", "f", "/":
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
