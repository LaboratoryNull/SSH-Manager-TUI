package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"ssh-manager-tui/internal/config"
	"ssh-manager-tui/internal/remote"
)

// Split screen shows up to four tabs at once. Every tab keeps its own
// connection, terminal and state, exactly as before; the split only decides
// which tabs share the screen (paneTabs) and how big each terminal is. The
// focused pane is the active tab, so keyboard input, prompts and dialogs
// work unchanged. Esc S adds a pane, Esc Z zooms the focused one.

const maxPanes = 4

// paneRect is the outer box of a pane in screen coordinates (row 0 is the
// app bar).
type paneRect struct{ x, y, w, h int }

// termSize is the terminal inside the box: borders and the title line off.
func (r paneRect) termSize() (int, int) { return max(1, r.w-2), max(1, r.h-3) }

func (r paneRect) contains(x, y int) bool {
	return x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h
}

func (m *Model) paneIndex(id uint64) int {
	return slices.Index(m.paneTabs, id)
}

// inSplit reports whether the given tab is laid out as a pane right now
// (it is part of a split and not zoomed to the full screen).
func (m *Model) inSplit(id uint64) bool {
	return len(m.paneTabs) >= 2 && m.paneIndex(id) >= 0 && m.zoomedTab != id
}

// gridVisible decides whether the screen shows the panes. Dialogs of the
// focused tab and its host screen still take the whole screen.
func (m *Model) gridVisible() bool {
	return m.inSplit(m.tabID) && (m.page == pageConnected || m.connecting) &&
		m.reconnect == nil && m.forwardModal == nil && m.sftpDialog == nil && m.picker == nil
}

// paneRects tiles the content area: 2 side by side (stacked when narrow),
// 3 as one tall pane plus two on the right, 4 as a 2×2 grid.
func (m *Model) paneRects() []paneRect {
	x, y, w, h := 0, 1, max(2, m.width), max(2, m.contentHeight())
	half := func(total int) (int, int) { return total / 2, total - total/2 }
	wide := w >= 100
	switch len(m.paneTabs) {
	case 2:
		if wide {
			left, right := half(w)
			return []paneRect{{x, y, left, h}, {x + left, y, right, h}}
		}
		top, bottom := half(h)
		return []paneRect{{x, y, w, top}, {x, y + top, w, bottom}}
	case 3:
		top, bottom := half(h)
		if wide {
			left, right := half(w)
			return []paneRect{{x, y, left, h}, {x + left, y, right, top}, {x + left, y + top, right, bottom}}
		}
		third := h / 3
		return []paneRect{{x, y, w, third}, {x, y + third, w, third}, {x, y + 2*third, w, h - 2*third}}
	case 4:
		left, right := half(w)
		top, bottom := half(h)
		return []paneRect{{x, y, left, top}, {x + left, y, right, top}, {x, y + top, left, bottom}, {x + left, y + top, right, bottom}}
	}
	return nil
}

// paneRectFor returns the box of a tab while it is shown as a pane.
func (m *Model) paneRectFor(id uint64) (paneRect, bool) {
	if !m.inSplit(id) {
		return paneRect{}, false
	}
	rects := m.paneRects()
	index := m.paneIndex(id)
	if index < 0 || index >= len(rects) {
		return paneRect{}, false
	}
	return rects[index], true
}

func (m *Model) tabStateByID(id uint64) (*tabState, int) {
	for index, state := range m.tabs {
		if state.id == id {
			return state, index
		}
	}
	return nil, -1
}

// resizePanes gives every pane terminal (and its remote PTY) the size of
// its box; the focused tab goes through resizeRemote as usual.
func (m *Model) resizePanes() {
	for _, id := range m.paneTabs {
		if id == m.tabID {
			continue
		}
		state, _ := m.tabStateByID(id)
		rect, ok := m.paneRectFor(id)
		if state == nil || !ok || state.term == nil {
			continue
		}
		w, h := rect.termSize()
		state.term.Resize(w, h)
		if state.conn != nil {
			_ = state.conn.Resize(w, h)
		}
	}
	if m.page == pageConnected {
		m.resizeRemote()
	}
}

// normalizePanes drops closed tabs and dissolves a split of one pane.
func (m *Model) normalizePanes() {
	m.paneTabs = slices.DeleteFunc(m.paneTabs, func(id uint64) bool {
		state, _ := m.tabStateByID(id)
		return state == nil && id != m.tabID
	})
	if len(m.paneTabs) < 2 {
		m.paneTabs, m.zoomedTab = nil, 0
	}
	if m.paneIndex(m.zoomedTab) < 0 {
		m.zoomedTab = 0
	}
}

// addPane puts an open tab next to the focused one and focuses it.
func (m *Model) addPane(id uint64) {
	if len(m.paneTabs) == 0 {
		m.paneTabs = []uint64{m.tabID}
	}
	if m.paneIndex(id) < 0 {
		m.paneTabs = append(m.paneTabs, id)
	}
	m.zoomedTab = 0
	m.leavePaneFiles()
	if _, index := m.tabStateByID(id); index >= 0 && id != m.tabID {
		m.switchTab(index)
	}
	m.resizePanes()
}

// connectInNewPane opens a tab for a saved server as a new pane.
func (m *Model) connectInNewPane(sessionIndex int) tea.Cmd {
	if sessionIndex < 0 || sessionIndex >= len(m.config.Sessions) {
		return nil
	}
	if len(m.paneTabs) == 0 {
		m.paneTabs = []uint64{m.tabID}
	}
	m.zoomedTab = 0
	m.leavePaneFiles()
	m.openTab()
	m.paneTabs = append(m.paneTabs, m.tabID)
	m.resizePanes()
	session := m.config.Sessions[sessionIndex]
	m.selected = sessionIndex
	resolved, err := m.config.ResolveSession(session)
	if err != nil {
		m.status = "Ошибка профиля: " + err.Error()
		return nil
	}
	m.connecting = true
	m.status = "Подключение к " + session.Host + "…"
	width, height := m.consoleSize()
	return connectCommand(m.tabID, resolved, width, height)
}

// detachPane takes the focused tab off the screen; its session stays open
// in its tab.
func (m *Model) detachPane() {
	index := m.paneIndex(m.tabID)
	if index < 0 {
		return
	}
	m.paneTabs = slices.Delete(m.paneTabs, index, index+1)
	next := uint64(0)
	if len(m.paneTabs) > 0 {
		next = m.paneTabs[min(index, len(m.paneTabs)-1)]
	}
	m.normalizePanes()
	m.resizeRemote()
	if _, tabIndex := m.tabStateByID(next); tabIndex >= 0 {
		m.switchTab(tabIndex)
	}
	m.resizePanes()
}

func (m *Model) toggleZoom() {
	if len(m.paneTabs) < 2 || m.paneIndex(m.tabID) < 0 {
		return
	}
	if m.zoomedTab == m.tabID {
		m.zoomedTab = 0
		m.leavePaneFiles()
	} else {
		m.zoomedTab = m.tabID
	}
	m.resizePanes()
}

// leavePaneFiles hides the file panel of the focused tab; files are shown
// only for a full-screen (zoomed) terminal.
func (m *Model) leavePaneFiles() {
	m.sftpHidden, m.filesFocused = true, false
}

// focusPane moves the focus by delta panes in reading order.
func (m *Model) focusPane(delta int) bool {
	index := m.paneIndex(m.tabID)
	if index < 0 || len(m.paneTabs) < 2 {
		return false
	}
	next := m.paneTabs[(index+delta+len(m.paneTabs))%len(m.paneTabs)]
	if _, tabIndex := m.tabStateByID(next); tabIndex >= 0 {
		m.switchTab(tabIndex)
		return true
	}
	return false
}

// openSplitPicker lists what can be put on the screen: open sessions that
// are not shown yet and saved servers to connect in a new pane.
func (m *Model) openSplitPicker() {
	if len(m.paneTabs) >= maxPanes {
		m.status = fmt.Sprintf("На экране уже %d терминала — это максимум", maxPanes)
		return
	}
	m.saveActiveTab()
	var actions []paletteAction
	for index, state := range m.tabs {
		if state.id == m.tabID || m.paneIndex(state.id) >= 0 || state.page != pageConnected {
			continue
		}
		id := state.id
		name := state.profile.Name
		if name == "" {
			name = state.profile.Host
		}
		actions = append(actions, paletteAction{
			title: "Показать рядом · " + name, shortcut: fmt.Sprintf("вкладка %d", index+1), group: "Открытые сессии",
			run: func(m *Model) (tea.Model, tea.Cmd) { m.addPane(id); return m, nil },
		})
	}
	for _, sessionIndex := range m.sessionOrder() {
		session := m.config.Sessions[sessionIndex]
		index := sessionIndex
		actions = append(actions, paletteAction{
			title: "Подключить рядом · " + session.Name, shortcut: m.sessionUser(session) + "@" + hostWithPort(session), group: "Серверы",
			run: func(m *Model) (tea.Model, tea.Cmd) { return m, m.connectInNewPane(index) },
		})
	}
	if len(actions) == 0 {
		m.status = "Нет других сессий и сохранённых серверов"
		return
	}
	m.openCommandPalette()
	m.palette.title = fmt.Sprintf("Добавить на экран · %d из %d", max(1, len(m.paneTabs))+1, maxPanes)
	m.palette.input.Placeholder = "Сервер или открытая сессия"
	m.palette.actions = actions
}

// paneSnapshot is what a pane needs to draw a tab.
type paneSnapshot struct {
	id           uint64
	number       int
	profile      config.Session
	term         *terminalScreen
	conn         *remote.Connection
	page         page
	connecting   bool
	reconnecting bool
	selection    *terminalSelection
}

func (m *Model) paneSnapshot(id uint64) (paneSnapshot, bool) {
	state, index := m.tabStateByID(id)
	if id == m.tabID {
		return paneSnapshot{
			id: id, number: m.activeTab + 1, profile: m.profile, term: m.term, conn: m.conn, page: m.page,
			connecting: m.connecting, reconnecting: m.reconnect != nil, selection: m.selection,
		}, true
	}
	if state == nil {
		return paneSnapshot{}, false
	}
	return paneSnapshot{
		id: id, number: index + 1, profile: state.profile, term: state.term, conn: state.conn, page: state.page,
		connecting: state.connecting, reconnecting: state.reconnect != nil,
	}, true
}

func (m *Model) gridView() string {
	rects := m.paneRects()
	panes := make([][]string, len(rects))
	for index, rect := range rects {
		panes[index] = strings.Split(m.paneView(m.paneTabs[index], rect), "\n")
	}
	// The panes tile the area, so each screen row is the panes covering it
	// concatenated from left to right.
	lines := make([]string, 0, m.contentHeight())
	for y := 1; y <= m.contentHeight(); y++ {
		var row strings.Builder
		for index, rect := range rects {
			if y >= rect.y && y < rect.y+rect.h {
				if line := y - rect.y; line < len(panes[index]) {
					row.WriteString(panes[index][line])
				}
			}
		}
		lines = append(lines, row.String())
	}
	return strings.Join(lines, "\n")
}

func (m *Model) paneView(id uint64, rect paneRect) string {
	termW, termH := rect.termSize()
	innerH := max(1, rect.h-2)
	snapshot, ok := m.paneSnapshot(id)
	focused := id == m.tabID
	border := borderMuted
	if focused {
		border = accent
	}

	name := snapshot.profile.Name
	if name == "" {
		name = snapshot.profile.Host
	}
	dotColor := successColor
	switch {
	case snapshot.reconnecting:
		dotColor = warningColor
	case snapshot.page != pageConnected:
		dotColor = muted
	}
	badgeStyle := lipgloss.NewStyle().Foreground(muted)
	nameStyle := lipgloss.NewStyle().Foreground(muted)
	if focused {
		badgeStyle = lipgloss.NewStyle().Foreground(contrastText).Background(accent).Bold(true)
		nameStyle = lipgloss.NewStyle().Foreground(textColor).Bold(true)
	}
	badge := badgeStyle.Render(fmt.Sprintf(" %d ", snapshot.number))
	dot := lipgloss.NewStyle().Foreground(dotColor).Render(" ● ")
	label := nameStyle.Render(ellipsize(name, max(4, termW/3)))
	address := ""
	if snapshot.profile.Host != "" {
		address = "  " + snapshot.profile.User + "@" + hostWithPort(snapshot.profile)
	}
	used := lipgloss.Width(badge) + lipgloss.Width(dot) + lipgloss.Width(label)
	readout := ""
	if snapshot.page == pageConnected && !snapshot.reconnecting {
		readout = telemetry(snapshot.conn)
	}
	room := max(0, termW-used-lipgloss.Width(readout)-3)
	title := withTelemetry(badge+dot+label+lipgloss.NewStyle().Foreground(muted).Render(ellipsize(address, room)), readout, termW)

	var body string
	switch {
	case !ok:
		body = ""
	case snapshot.reconnecting:
		body = paneMessage("Переподключение…", termW, termH)
	case snapshot.page == pageConnected && snapshot.term != nil:
		body = snapshot.term.RenderANSISelection(snapshot.selection)
	case snapshot.connecting:
		body = paneMessage("Подключение к "+snapshot.profile.Host+"…", termW, termH)
	default:
		body = paneMessage("Нет подключения · Esc W — закрыть панель", termW, termH)
	}
	content := lipgloss.NewStyle().Width(termW).Height(innerH).MaxHeight(innerH).Render(title + "\n" + body)
	return borderStyle.Copy().BorderForeground(border).Width(rect.w).Height(rect.h).MaxHeight(rect.h).Render(content)
}

func paneMessage(text string, width, height int) string {
	lines := make([]string, max(1, height))
	lines[len(lines)/2] = lipgloss.NewStyle().Foreground(muted).Render(centerText(ellipsize(text, width), width))
	return strings.Join(lines, "\n")
}

// paneAt returns the tab id of the pane under a screen point.
func (m *Model) paneAt(x, y int) (uint64, paneRect, bool) {
	if !m.gridVisible() {
		return 0, paneRect{}, false
	}
	for index, rect := range m.paneRects() {
		if rect.contains(x, y) {
			return m.paneTabs[index], rect, true
		}
	}
	return 0, paneRect{}, false
}
