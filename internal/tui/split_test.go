package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"ssh-manager-tui/internal/config"
)

// splitModel builds n connected tabs (without network connections).
func splitModel(t *testing.T, width, height, n int) *Model {
	t.Helper()
	m := &Model{width: width, height: height, tabID: 1, nextTabID: 1, page: pageSessions, sftpHidden: true}
	m.resetForm()
	m.tabs = []*tabState{m.captureTab()}
	connect := func(name string) {
		m.page = pageConnected
		m.profile = config.Session{Name: name, Host: name + ".example", Port: 22, User: "deploy"}
		w, h := m.consoleSize()
		m.term = newTerminalScreen(w, h)
		m.term.Feed([]byte("$ hostname\r\n" + name + "\r\n$ "))
	}
	connect("srv1")
	for index := 2; index <= n; index++ {
		m.openTab()
		connect(fmt.Sprintf("srv%d", index))
	}
	m.saveActiveTab()
	m.switchTab(0)
	for _, state := range m.tabs[1:] {
		m.addPane(state.id)
	}
	// addPane focuses the added pane; start the tests from the first one.
	m.switchTab(0)
	return m
}

func TestPaneRectsTileTheScreen(t *testing.T) {
	for _, size := range [][2]int{{160, 48}, {100, 30}, {80, 24}} {
		for n := 2; n <= maxPanes; n++ {
			m := &Model{width: size[0], height: size[1], paneTabs: make([]uint64, n)}
			covered := make(map[[2]int]int)
			for _, rect := range m.paneRects() {
				for y := rect.y; y < rect.y+rect.h; y++ {
					for x := rect.x; x < rect.x+rect.w; x++ {
						covered[[2]int{x, y}]++
					}
				}
			}
			if len(covered) != m.width*m.contentHeight() {
				t.Fatalf("%dx%d with %d panes covers %d cells, want %d", size[0], size[1], n, len(covered), m.width*m.contentHeight())
			}
			for cell, count := range covered {
				if count != 1 || cell[1] < 1 || cell[1] > m.contentHeight() {
					t.Fatalf("%dx%d with %d panes: cell %v covered %d times", size[0], size[1], n, cell, count)
				}
			}
		}
	}
}

func TestSplitShowsFourTerminalsAndKeepsTheFrameExact(t *testing.T) {
	m := splitModel(t, 140, 40, 4)
	if len(m.paneTabs) != 4 || !m.gridVisible() {
		t.Fatalf("panes = %v, grid = %v", m.paneTabs, m.gridVisible())
	}
	view := m.viewContent()
	lines := strings.Split(view, "\n")
	if len(lines) != m.height {
		t.Fatalf("frame has %d lines, want %d", len(lines), m.height)
	}
	for index, line := range lines {
		if width := lipgloss.Width(line); width != m.width {
			t.Fatalf("line %d is %d wide, want %d", index, width, m.width)
		}
	}
	for _, name := range []string{"srv1", "srv2", "srv3", "srv4"} {
		if strings.Count(view, name) < 2 { // title and terminal output
			t.Fatalf("%s is not shown in its pane", name)
		}
	}
	// Each background pane terminal has the size of its box.
	rects := m.paneRects()
	for index, id := range m.paneTabs {
		w, h := rects[index].termSize()
		term := m.term
		if id != m.tabID {
			state, _ := m.tabStateByID(id)
			term = state.term
		}
		if term.w != w || term.h != h {
			t.Fatalf("pane %d terminal = %dx%d, want %dx%d", index, term.w, term.h, w, h)
		}
	}
}

func TestSplitFocusZoomAndClose(t *testing.T) {
	m := splitModel(t, 140, 40, 3)
	first := m.tabID
	escape := func(key tea.Key) {
		m.handleConnectedKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
		m.handleConnectedKey(tea.KeyPressMsg(key))
	}
	escape(tea.Key{Code: tea.KeyRight})
	if m.tabID == first || m.paneIndex(m.tabID) != 1 {
		t.Fatalf("Esc → focused tab %d", m.tabID)
	}
	escape(tea.Key{Code: 'z', Text: "z"})
	if m.gridVisible() || m.zoomedTab != m.tabID {
		t.Fatal("Esc z did not zoom the focused pane")
	}
	if w, _ := m.consoleSize(); w != m.width-2 {
		t.Fatalf("zoomed terminal width = %d, want %d", w, m.width-2)
	}
	escape(tea.Key{Code: 'z', Text: "z"})
	if !m.gridVisible() {
		t.Fatal("second Esc z did not restore the split")
	}

	// Clicking another pane focuses it.
	rect := m.paneRects()[2]
	m.handleMouseClick(tea.MouseClickMsg(tea.Mouse{X: rect.x + 3, Y: rect.y + 3, Button: tea.MouseLeft}))
	if m.paneIndex(m.tabID) != 2 {
		t.Fatalf("click focused pane %d", m.paneIndex(m.tabID))
	}

	// Closing panes shrinks the split and dissolves it at one pane.
	escape(tea.Key{Code: 'w', Text: "w"})
	if len(m.paneTabs) != 2 || m.paneIndex(m.tabID) < 0 {
		t.Fatalf("after closing a pane: panes=%v focus=%d", m.paneTabs, m.tabID)
	}
	escape(tea.Key{Code: 'w', Text: "w"})
	if len(m.paneTabs) != 0 || m.gridVisible() {
		t.Fatalf("split with one pane left = %v", m.paneTabs)
	}
	if w, _ := m.consoleSize(); w != m.width-2 {
		t.Fatalf("single terminal width = %d, want full width", w)
	}
}

func TestSplitPickerConnectsSavedServerInNewPane(t *testing.T) {
	m := splitModel(t, 140, 40, 1)
	m.config.Sessions = []config.Session{{Name: "db", Host: "db.example", Port: 22, User: "postgres"}}
	m.openSplitPicker()
	if m.palette == nil || !strings.Contains(m.palette.title, "Добавить на экран") {
		t.Fatal("Esc s did not open the split picker")
	}
	_, cmd := m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd == nil || !m.connecting || len(m.paneTabs) != 2 || m.paneIndex(m.tabID) != 1 {
		t.Fatalf("new pane: cmd=%v connecting=%v panes=%v", cmd != nil, m.connecting, m.paneTabs)
	}
	if !m.gridVisible() || !strings.Contains(m.viewContent(), "Подключение к db.example") {
		t.Fatal("connecting pane is not shown in the split")
	}
	want, _ := m.paneRects()[1].termSize()
	if w, _ := m.consoleSize(); w != want {
		t.Fatalf("new pane connects with width %d, want %d", w, want)
	}
}

func TestSplitAllowsAtMostFourPanes(t *testing.T) {
	m := splitModel(t, 140, 40, 4)
	m.config.Sessions = []config.Session{{Name: "db", Host: "db.example", Port: 22, User: "postgres"}}
	m.openSplitPicker()
	if m.palette != nil || !strings.Contains(m.status, "максимум") {
		t.Fatalf("fifth pane: palette=%v status=%q", m.palette != nil, m.status)
	}
}
