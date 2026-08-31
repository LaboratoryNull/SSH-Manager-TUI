package tui

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

var clickableHintLabels = []string{
	"Ctrl+Shift+C", "Ctrl+Space", "Alt+1…9", "Alt+←/→", "←/Backspace",
	"Tab/↑/↓", "n/н/Esc", "Ctrl+T", "Ctrl+W", "Ctrl+B", "Ctrl+D",
	"Ctrl+U", "Ctrl+X", "Alt←/→", "Esc q", "n/e/d", "Alt+P", "AltP",
	"^Space", "Enter", "Space", "Backspace", "↑/↓", "←/→", "y/д",
	"shift+tab", "Tab", "Esc", "F5", "^T", "^W", "^B", "^D", "^U",
	"^X", "n", "e", "d", "g", "q", "s", "u", "/",
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
		fileW, _, _, _, _ := m.layout()
		if mouse.Y > 0 {
			if !m.sftpHidden && mouse.X < fileW {
				m.filesFocused = true
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

func (m *Model) clickedTab(x int) int {
	used := 0
	helpWidth := runewidth.StringWidth(ansi.Strip(renderHints(
		[2]string{"^T", "+"}, [2]string{"^W", "×"}, [2]string{"Alt←/→", "вкладки"},
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
	var press tea.KeyPressMsg
	switch label {
	case "Ctrl+T", "^T":
		press = key('t', tea.ModCtrl, "")
	case "Ctrl+W", "^W":
		press = key('w', tea.ModCtrl, "")
	case "Ctrl+B", "^B":
		press = key('b', tea.ModCtrl, "")
	case "Ctrl+D", "^D":
		press = key('d', tea.ModCtrl, "")
	case "Ctrl+U", "^U":
		press = key('u', tea.ModCtrl, "")
	case "Ctrl+X", "^X":
		press = key('x', tea.ModCtrl, "")
	case "Ctrl+Space", "^Space":
		press = key(tea.KeySpace, tea.ModCtrl, "")
	case "Ctrl+Shift+C":
		press = key('c', tea.ModCtrl|tea.ModShift, "")
	case "Alt+P", "AltP":
		press = key('p', tea.ModAlt, "")
	case "Alt+←/→", "Alt←/→", "Alt+1…9":
		if len(m.tabs) > 1 {
			m.switchTab((m.activeTab + 1) % len(m.tabs))
		}
		return m, nil
	case "Esc q":
		if m.page == pageConnected {
			m.disconnect("Отключено")
		}
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
	case "F5":
		press = key(tea.KeyF5, 0, "")
	case "y/д":
		press = key('y', 0, "y")
	case "n/e/d":
		press = key('n', 0, "n")
	case "n", "e", "d", "g", "q", "s", "u", "/":
		r := []rune(label)[0]
		press = key(r, 0, string(r))
	default:
		return m, nil
	}
	if model, cmd, handled := m.handleTabKey(press); handled {
		return model, cmd
	}
	return m.handleKey(press)
}
