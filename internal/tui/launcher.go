package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"ssh-manager-tui/internal/config"
)

// The host screen is a launcher: a search field over saved servers, recent
// connections and destinations found in shell history. Typing filters the
// list, Enter connects, and a full address connects to a new server that is
// saved automatically once the connection succeeds.

const (
	launcherRecentLimit       = 3
	launcherRecentMinSessions = 6
	launcherHistoryLimit      = 5
	launcherMaxWidth          = 78
)

type launcherState struct {
	query  textinput.Model
	cursor int
	offset int
	// lineRows maps each rendered list line to its row (-1 for headers),
	// so mouse clicks can be resolved without re-deriving the layout.
	lineRows []int
}

type launchKind int

const (
	launchSaved launchKind = iota
	launchHistory
	launchQuick
)

type launchRow struct {
	kind    launchKind
	section string
	index   int
	session config.Session
	uses    int
	score   int
}

type historyHostsMsg struct{ hosts []historyHost }

func loadHistoryHosts() tea.Msg {
	commands, _, err := readShellHistory()
	if err != nil {
		return historyHostsMsg{}
	}
	return historyHostsMsg{hosts: historyHostsFromCommands(commands)}
}

func (m *Model) ensureLauncher() *launcherState {
	if m.launcher == nil {
		input := textinput.New()
		input.Prompt = ""
		input.Placeholder = "Найти или ввести user@host"
		input.CharLimit = 256
		input.Focus()
		m.launcher = &launcherState{query: input}
	}
	return m.launcher
}

func (m *Model) launcherQuery() string {
	if m.launcher == nil {
		return ""
	}
	return strings.TrimSpace(m.launcher.query.Value())
}

// launchRows builds the list for the current query. Without a query it shows
// recent servers (for longer lists), groups and a few hosts from history;
// with a query it is a single list ranked by match quality.
func (m *Model) launchRows() []launchRow {
	query := m.launcherQuery()
	saved := make(map[string]bool, len(m.config.Sessions))
	for _, session := range m.config.Sessions {
		resolved := session
		resolved.User = m.sessionUser(session)
		saved[sessionIdentity(resolved)] = true
	}
	var history []historyHost
	for _, host := range m.historyHosts {
		if !saved[sessionIdentity(host.session)] {
			history = append(history, host)
		}
	}
	if query == "" {
		return m.browseRows(history)
	}

	var rows []launchRow
	for index, session := range m.config.Sessions {
		if score := sessionMatchScore(query, session, m.sessionUser(session)); score > 0 {
			rows = append(rows, launchRow{kind: launchSaved, index: index, session: session, score: score + 5})
		}
	}
	for _, host := range history {
		if score := sessionMatchScore(query, host.session, host.session.User); score > 0 {
			rows = append(rows, launchRow{kind: launchHistory, session: host.session, uses: host.uses, score: score})
		}
	}
	sort.SliceStable(rows, func(a, b int) bool {
		if rows[a].score != rows[b].score {
			return rows[a].score > rows[b].score
		}
		return rows[a].session.ConnectionCount+uint64(rows[a].uses) > rows[b].session.ConnectionCount+uint64(rows[b].uses)
	})
	if target, ok := parseQuickTarget(query); ok {
		known := saved[sessionIdentity(target)]
		for _, host := range history {
			known = known || sessionIdentity(host.session) == sessionIdentity(target)
		}
		if !known {
			// A half-typed "127.0" or "prod.ex" already parses as an address;
			// it only goes first when the intent is explicit, otherwise the
			// matching servers keep priority and the new address follows.
			quick := launchRow{kind: launchQuick, session: target}
			if strings.Contains(query, "@") || strings.HasPrefix(query, "ssh ") || len(rows) == 0 {
				rows = append([]launchRow{quick}, rows...)
			} else {
				rows = append(rows, quick)
			}
		}
	}
	return rows
}

func (m *Model) browseRows(history []historyHost) []launchRow {
	var rows []launchRow
	shown := make(map[int]bool)
	if len(m.config.Sessions) >= launcherRecentMinSessions {
		var recent []int
		for index, session := range m.config.Sessions {
			if !session.LastConnected.IsZero() {
				recent = append(recent, index)
			}
		}
		sort.SliceStable(recent, func(a, b int) bool {
			return m.config.Sessions[recent[a]].LastConnected.After(m.config.Sessions[recent[b]].LastConnected)
		})
		for _, index := range recent[:min(launcherRecentLimit, len(recent))] {
			rows = append(rows, launchRow{kind: launchSaved, section: "Недавние", index: index, session: m.config.Sessions[index]})
			shown[index] = true
		}
	}

	grouped := false
	for _, session := range m.config.Sessions {
		grouped = grouped || strings.TrimSpace(session.Group) != ""
	}
	order := m.sessionOrder()
	// Named groups first, alphabetically; servers without a group close the
	// list under "Другие" (or without any header when nothing is grouped).
	sort.SliceStable(order, func(a, b int) bool {
		left := strings.TrimSpace(m.config.Sessions[order[a]].Group) == ""
		right := strings.TrimSpace(m.config.Sessions[order[b]].Group) == ""
		return !left && right
	})
	for _, index := range order {
		if shown[index] {
			continue
		}
		session := m.config.Sessions[index]
		section := strings.TrimSpace(session.Group)
		if section == "" && (grouped || len(rows) > 0) {
			section = "Другие"
		}
		rows = append(rows, launchRow{kind: launchSaved, section: section, index: index, session: session})
	}

	sort.SliceStable(history, func(a, b int) bool { return history[a].uses > history[b].uses })
	for _, host := range history[:min(launcherHistoryLimit, len(history))] {
		rows = append(rows, launchRow{kind: launchHistory, section: "Найдено в истории", session: host.session, uses: host.uses})
	}
	return rows
}

// parseQuickTarget accepts "user@host", "host:port", "ssh://…" or a pasted
// "ssh -p 2222 user@host" command.
func parseQuickTarget(query string) (config.Session, bool) {
	query = strings.TrimSpace(query)
	if strings.HasPrefix(query, "ssh ") {
		sessions := sshSessionsFromCommand(query)
		if len(sessions) == 1 {
			return sessions[0], true
		}
		return config.Session{}, false
	}
	if strings.ContainsAny(query, " \t") || !(strings.ContainsAny(query, "@.:") || query == "localhost") {
		return config.Session{}, false
	}
	host, userName, port, ok := parseSSHTarget(query)
	if !ok || strings.HasSuffix(host, ".") || strings.HasPrefix(host, ".") || strings.ContainsAny(host, "@/") {
		return config.Session{}, false
	}
	if port == 0 {
		port = 22
	}
	if userName == "" {
		userName = currentUser()
	}
	if port < 1 || port > 65535 || userName == "" {
		return config.Session{}, false
	}
	return config.Session{Name: host, Host: host, Port: port, User: userName}, true
}

// sessionMatchScore ranks a server against the query; 0 means no match. The
// name counts most, then the address, then the group.
func sessionMatchScore(query string, session config.Session, userName string) int {
	address := userName + "@" + session.Host
	best := 0
	for _, field := range []struct {
		text  string
		bonus int
	}{
		{session.Name, 20}, {session.Host, 10}, {address, 5}, {session.Group, 0},
	} {
		if score := fuzzyScore(query, field.text); score > 0 {
			best = max(best, score+field.bonus)
		}
	}
	return best
}

// fuzzyScore matches query as a case-insensitive subsequence of text.
// Substrings beat scattered matches; prefixes and word starts score higher.
func fuzzyScore(query, text string) int {
	q := []rune(strings.ToLower(query))
	t := []rune(strings.ToLower(text))
	if len(q) == 0 || len(t) == 0 {
		return 0
	}
	if index := strings.Index(string(t), string(q)); index >= 0 {
		position := len([]rune(string(t)[:index]))
		score := 200 - min(position, 50)
		if position == 0 {
			score += 60
		} else if isWordBoundary(t[position-1]) {
			score += 30
		}
		return score
	}
	score, ti, previous := 0, 0, -2
	for _, r := range q {
		for ti < len(t) && t[ti] != r {
			ti++
		}
		if ti == len(t) {
			return 0
		}
		score += 4
		if ti == previous+1 {
			score += 6
		}
		if ti == 0 || isWordBoundary(t[ti-1]) {
			score += 8
		}
		previous = ti
		ti++
	}
	return max(1, min(score, 150)-len(t)/8)
}

func isWordBoundary(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r)
}

func (m *Model) selectedLaunchRow() (launchRow, bool) {
	rows := m.launchRows()
	launcher := m.ensureLauncher()
	if len(rows) == 0 {
		return launchRow{}, false
	}
	launcher.cursor = clamp(launcher.cursor, 0, len(rows)-1)
	return rows[launcher.cursor], true
}

// syncSelection keeps m.selected (used by edit, move and delete) pointing at
// the highlighted saved server.
func (m *Model) syncSelection() {
	if row, ok := m.selectedLaunchRow(); ok && row.kind == launchSaved {
		m.selected = row.index
	}
}

func (m *Model) moveLauncherCursor(delta int) {
	rows := m.launchRows()
	launcher := m.ensureLauncher()
	if len(rows) == 0 {
		launcher.cursor = 0
		return
	}
	launcher.cursor = clamp(launcher.cursor+delta, 0, len(rows)-1)
	m.syncSelection()
}

func (m *Model) handleSessionKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	launcher := m.ensureLauncher()
	if m.connecting {
		if key.String() == "esc" {
			m.status = "Ожидание сетевого подключения нельзя прервать мгновенно"
		}
		return m, nil
	}
	if m.confirm {
		switch key.String() {
		case "enter", "y", "Y", "д", "Д":
			m.deleteSelectedSession()
		case "esc", "n", "N", "н", "Н":
			m.confirm = false
			m.status = ""
		}
		return m, nil
	}
	switch key.String() {
	case "ctrl+c":
		m.close()
		return m, tea.Quit
	case "up", "ctrl+p":
		m.moveLauncherCursor(-1)
		return m, nil
	case "down":
		m.moveLauncherCursor(1)
		return m, nil
	case "ctrl+n":
		m.openNewSessionForm()
		return m, nil
	case "pgup":
		m.moveLauncherCursor(-10)
		return m, nil
	case "pgdown":
		m.moveLauncherCursor(10)
		return m, nil
	case "enter":
		return m, m.connectLaunchRow()
	case "esc":
		if launcher.query.Value() != "" {
			launcher.query.SetValue("")
			launcher.cursor, launcher.offset = 0, 0
			m.syncSelection()
		}
		m.status = ""
		return m, nil
	case "ctrl+k":
		m.openCommandPalette()
		return m, nil
	case "ctrl+e":
		m.editSelectedSession()
		return m, nil
	case "ctrl+g":
		m.moveSelectedSessionToGroup()
		return m, nil
	case "delete":
		if launcher.query.Value() == "" {
			m.askDeleteSelectedSession()
			return m, nil
		}
	}
	before := launcher.query.Value()
	var cmd tea.Cmd
	launcher.query, cmd = launcher.query.Update(key)
	if launcher.query.Value() != before {
		launcher.cursor, launcher.offset = 0, 0
		m.status = ""
		m.syncSelection()
	}
	return m, cmd
}

func (m *Model) handleLauncherPaste(content string) (tea.Model, tea.Cmd) {
	launcher := m.ensureLauncher()
	content = strings.TrimSpace(strings.ReplaceAll(content, "\n", " "))
	launcher.query.SetValue(launcher.query.Value() + content)
	launcher.query.CursorEnd()
	launcher.cursor, launcher.offset = 0, 0
	m.syncSelection()
	return m, nil
}

func (m *Model) connectLaunchRow() tea.Cmd {
	row, ok := m.selectedLaunchRow()
	if !ok {
		if query := m.launcherQuery(); query != "" {
			m.status = "Не найдено. Введите адрес целиком, например deploy@example.com"
		}
		return nil
	}
	session := row.session
	m.pendingSave = nil
	if row.kind == launchSaved {
		m.selected = row.index
	} else {
		session.Name = m.uniqueSessionName(session.Host)
		pending := session
		m.pendingSave = &pending
	}
	resolved, err := m.config.ResolveSession(session)
	if err != nil {
		m.pendingSave = nil
		m.status = "Ошибка профиля: " + err.Error()
		return nil
	}
	m.connecting = true
	m.status = "Подключение к " + session.Host + "…"
	width, height := m.consoleSize()
	return connectCommand(m.tabID, resolved, width, height)
}

// savePendingHost stores a server picked from history or typed as an address
// after the first successful connection, so the user never fills a form for
// it. It returns the profile under the name it was saved with.
func (m *Model) savePendingHost(profile config.Session) (config.Session, bool, error) {
	pending := m.pendingSave
	if pending == nil || pending.Name != profile.Name {
		return profile, false, nil
	}
	m.pendingSave = nil
	for index, session := range m.config.Sessions {
		resolved := session
		resolved.User = m.sessionUser(session)
		if sessionIdentity(resolved) == sessionIdentity(profile) {
			// Saved meanwhile, for example from another tab.
			profile.Name = session.Name
			m.selected = index
			return profile, false, nil
		}
	}
	session := *pending
	session.Name = m.uniqueSessionName(session.Host)
	session.Password = profile.Password
	m.config.Sessions = append(m.config.Sessions, session)
	if m.store != nil {
		if err := m.store.Save(m.config); err != nil {
			m.config.Sessions = m.config.Sessions[:len(m.config.Sessions)-1]
			return profile, false, err
		}
	}
	m.selected = len(m.config.Sessions) - 1
	profile.Name = session.Name
	return profile, true, nil
}

func (m *Model) openNewSessionForm() {
	query := m.launcherQuery()
	m.resetForm()
	if target, ok := parseQuickTarget(query); ok {
		m.inputs[hostNameInput].SetValue(m.uniqueSessionName(target.Host))
		m.inputs[hostAddressInput].SetValue(target.Host)
		m.inputs[hostPortInput].SetValue(fmt.Sprint(target.Port))
		m.inputs[hostUserInput].SetValue(target.User)
	}
	m.page, m.status = pageForm, ""
}

func (m *Model) selectedSavedRow() (launchRow, bool) {
	row, ok := m.selectedLaunchRow()
	if !ok || row.kind != launchSaved {
		return launchRow{}, false
	}
	m.selected = row.index
	return row, true
}

func (m *Model) editSelectedSession() {
	if _, ok := m.selectedSavedRow(); !ok {
		m.status = "Выберите сохранённый сервер"
		return
	}
	m.beginSessionEdit()
}

func (m *Model) moveSelectedSessionToGroup() {
	if _, ok := m.selectedSavedRow(); !ok {
		m.status = "Выберите сохранённый сервер"
		return
	}
	m.beginGroupEdit()
}

func (m *Model) askDeleteSelectedSession() {
	if _, ok := m.selectedSavedRow(); !ok {
		return
	}
	m.confirm = true
	m.status = ""
}

func (m *Model) deleteSelectedSession() {
	m.confirm = false
	if m.selected < 0 || m.selected >= len(m.config.Sessions) {
		return
	}
	removed := m.config.Sessions[m.selected]
	sessions := append([]config.Session(nil), m.config.Sessions...)
	m.config.Sessions = append(m.config.Sessions[:m.selected:m.selected], m.config.Sessions[m.selected+1:]...)
	if m.store != nil {
		if err := m.store.Save(m.config); err != nil {
			m.config.Sessions = sessions
			m.status = "Не удалось сохранить: " + err.Error()
			return
		}
	}
	m.status = "«" + removed.Name + "» удалён"
	launcher := m.ensureLauncher()
	launcher.cursor = max(0, launcher.cursor-1)
	m.selected = 0
	m.syncSelection()
}

// saveSelectedRow stores a history or typed server without connecting.
func (m *Model) saveSelectedRow() {
	row, ok := m.selectedLaunchRow()
	if !ok || row.kind == launchSaved {
		return
	}
	session := row.session
	session.Name = m.uniqueSessionName(session.Host)
	m.config.Sessions = append(m.config.Sessions, session)
	if m.store != nil {
		if err := m.store.Save(m.config); err != nil {
			m.config.Sessions = m.config.Sessions[:len(m.config.Sessions)-1]
			m.status = "Не удалось сохранить: " + err.Error()
			return
		}
	}
	m.selected = len(m.config.Sessions) - 1
	m.status = "«" + session.Name + "» добавлен"
}

// ── View ────────────────────────────────────────────────────────────────

type launcherLine struct {
	text string
	row  int // index into rows, or -1 for headers and spacing
}

// launcherChrome is the number of box lines around the list: border,
// padding, title, search, rule, spacer, status and hints.
const launcherChrome = 13

func (m *Model) sessionsView() string {
	launcher := m.ensureLauncher()
	boxWidth := min(launcherMaxWidth, max(40, m.width-4))
	inner := boxWidth - 8
	rows := m.launchRows()
	launcher.cursor = clamp(launcher.cursor, 0, max(0, len(rows)-1))
	if len(rows) > 0 && rows[launcher.cursor].kind == launchSaved {
		m.selected = rows[launcher.cursor].index
	}

	count := ""
	if n := len(m.config.Sessions); n > 0 {
		count = fmt.Sprintf("%d %s", n, ruPlural(n, "сервер", "сервера", "серверов"))
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(textColor).Render("Серверы")
	header := title + strings.Repeat(" ", max(1, inner-lipgloss.Width(title)-lipgloss.Width(count))) +
		lipgloss.NewStyle().Foreground(muted).Render(count)

	styleSearch(&launcher.query, inner-3)
	search := lipgloss.NewStyle().Foreground(accentAlt).Bold(true).Render("›") + "  " + launcher.query.View()
	rule := lipgloss.NewStyle().Foreground(borderMuted).Render(strings.Repeat("─", inner))

	// Like Spotlight the search field stays put: the box is anchored at the
	// position it has with the tallest list, and only grows or shrinks
	// downwards as results change.
	maxList := clamp(m.contentHeight()-launcherChrome, 3, 16)
	list, below := m.launcherList(rows, inner, maxList)
	spacer := ""
	if below > 0 {
		more := fmt.Sprintf("ещё %d ↓", below)
		spacer = strings.Repeat(" ", max(0, inner-lipgloss.Width(more)-1)) + lipgloss.NewStyle().Foreground(muted).Render(more)
	}
	status := ""
	if m.status != "" && !m.confirm {
		status = statusLine(ellipsize(m.status, inner-2))
	}
	lines := []string{header, "", search, rule}
	lines = append(lines, list...)
	lines = append(lines, spacer, status, m.launcherFooter(rows, inner))
	box := borderStyle.Copy().BorderForeground(borderMuted).Padding(1, 3).Width(boxWidth).Render(strings.Join(lines, "\n"))
	top := max(0, (m.contentHeight()-(maxList+launcherChrome))/2)
	return m.placeAt(box, top)
}

// placeAt centres content horizontally with a fixed top offset.
func (m *Model) placeAt(content string, top int) string {
	whitespace := lipgloss.WithWhitespaceStyle(lipgloss.NewStyle().Background(background))
	body := lipgloss.PlaceHorizontal(m.width, lipgloss.Center, content, whitespace)
	blank := lipgloss.NewStyle().Background(background).Width(m.width).Render("")
	lines := make([]string, 0, m.contentHeight())
	for range top {
		lines = append(lines, blank)
	}
	lines = append(lines, strings.Split(body, "\n")...)
	for len(lines) < m.contentHeight() {
		lines = append(lines, blank)
	}
	return strings.Join(lines[:min(len(lines), max(1, m.contentHeight()))], "\n")
}

// launcherList renders up to maxHeight list lines (at least a few, so the
// box keeps a calm size) and reports how many rows are hidden below.
func (m *Model) launcherList(rows []launchRow, width, maxHeight int) ([]string, int) {
	launcher := m.ensureLauncher()
	minHeight := min(6, maxHeight)
	if len(rows) == 0 {
		launcher.lineRows = nil
		empty := m.launcherEmptyState(width)
		return padLines(empty, clamp(len(empty), minHeight, maxHeight)), 0
	}
	nameWidth := 12
	for _, row := range rows {
		if row.kind != launchQuick {
			nameWidth = max(nameWidth, lipgloss.Width(launchRowName(row))+2)
		}
	}
	nameWidth = min(nameWidth, max(12, width*2/5))
	var lines []launcherLine
	selectedLine := 0
	previous := "\x00"
	for index, row := range rows {
		if row.section != previous && row.section != "" {
			if len(lines) > 0 {
				lines = append(lines, launcherLine{row: -1})
			}
			lines = append(lines, launcherLine{
				text: lipgloss.NewStyle().Foreground(muted).Bold(true).Render(" " + strings.ToUpper(row.section)),
				row:  -1,
			})
		}
		previous = row.section
		if index == launcher.cursor {
			selectedLine = len(lines)
		}
		lines = append(lines, launcherLine{text: m.launcherRowView(row, index == launcher.cursor, width, nameWidth), row: index})
	}
	height := clamp(len(lines), minHeight, maxHeight)
	// Keep the highlighted row visible; when scrolling up, also show its
	// section header.
	if selectedLine < launcher.offset {
		launcher.offset = selectedLine
		if selectedLine > 0 && lines[selectedLine-1].row < 0 && lines[selectedLine-1].text != "" {
			launcher.offset--
		}
	}
	if selectedLine >= launcher.offset+height {
		launcher.offset = selectedLine - height + 1
	}
	launcher.offset = clamp(launcher.offset, 0, max(0, len(lines)-height))
	end := min(len(lines), launcher.offset+height)
	visible := make([]string, 0, height)
	launcher.lineRows = launcher.lineRows[:0]
	below := 0
	for _, line := range lines[end:] {
		if line.row >= 0 {
			below++
		}
	}
	for _, line := range lines[launcher.offset:end] {
		visible = append(visible, line.text)
		launcher.lineRows = append(launcher.lineRows, line.row)
	}
	return padLines(visible, height), below
}

// launcherRowAt maps a list line (0 = first visible line) to a row index.
func (m *Model) launcherRowAt(line int) int {
	if m.launcher == nil || line < 0 || line >= len(m.launcher.lineRows) {
		return -1
	}
	return m.launcher.lineRows[line]
}

func launchRowName(row launchRow) string {
	if row.kind == launchHistory {
		return row.session.Host
	}
	return row.session.Name
}

func (m *Model) launcherRowView(row launchRow, selected bool, width, nameWidth int) string {
	name, detail, meta := launchRowName(row), "", ""
	switch row.kind {
	case launchSaved:
		detail = m.sessionUser(row.session) + "@" + hostWithPort(row.session)
		if m.launcherQuery() != "" {
			meta = strings.TrimSpace(row.session.Group)
		} else if !row.session.LastConnected.IsZero() {
			meta = relativeTime(row.session.LastConnected, time.Now())
		}
	case launchHistory:
		detail = row.session.User + portSuffix(row.session.Port)
		if m.launcherQuery() == "" {
			meta = fmt.Sprintf("%d %s", row.uses, ruPlural(row.uses, "раз", "раза", "раз"))
		} else {
			meta = "из истории"
		}
	case launchQuick:
		meta = "сохранится после входа"
	}
	if selected && m.connecting {
		meta = "подключение…"
	}
	right := meta + " "

	if row.kind == launchQuick {
		// One sentence instead of columns: what Enter will do.
		lead := "Подключиться к "
		target := ellipsize(row.session.User+"@"+hostWithPort(row.session), max(4, width-2-lipgloss.Width(lead)-lipgloss.Width(right)-1))
		gap := max(1, width-1-lipgloss.Width(lead)-lipgloss.Width(target)-lipgloss.Width(right))
		if selected {
			return lipgloss.NewStyle().Background(accent).Foreground(contrastText).Bold(true).
				Render(" " + lead + target + strings.Repeat(" ", gap) + right)
		}
		return " " + lipgloss.NewStyle().Foreground(textColor).Render(lead) +
			lipgloss.NewStyle().Foreground(accentAlt).Bold(true).Render(target) + strings.Repeat(" ", gap) +
			lipgloss.NewStyle().Foreground(muted).Render(right)
	}

	nameText := padRight(ellipsize(name, nameWidth-1), nameWidth)
	detailWidth := max(0, width-1-nameWidth-lipgloss.Width(right)-1)
	detailText := ellipsize(detail, detailWidth)
	gap := max(1, width-1-nameWidth-lipgloss.Width(detailText)-lipgloss.Width(right))
	if selected {
		return lipgloss.NewStyle().Background(accent).Foreground(contrastText).Bold(true).
			Render(" " + nameText + detailText + strings.Repeat(" ", gap) + right)
	}
	return " " + lipgloss.NewStyle().Foreground(textColor).Render(nameText) +
		lipgloss.NewStyle().Foreground(muted).Render(detailText) + strings.Repeat(" ", gap) +
		lipgloss.NewStyle().Foreground(borderMuted).Render(right)
}

func (m *Model) launcherEmptyState(width int) []string {
	center := func(text string, style lipgloss.Style) string {
		return style.Render(centerText(truncate(text, width), width))
	}
	plain := lipgloss.NewStyle().Foreground(textColor)
	quiet := lipgloss.NewStyle().Foreground(muted)
	if m.launcherQuery() != "" {
		return []string{"", center("Ничего не найдено", plain), "", center("Введите адрес целиком — например, deploy@example.com", quiet)}
	}
	return []string{
		"",
		center("Здесь появятся ваши серверы", plain.Bold(true)),
		"",
		center("Введите адрес — например, deploy@example.com —", quiet),
		center("и нажмите Enter. Сервер сохранится сам.", quiet),
	}
}

func (m *Model) launcherFooter(rows []launchRow, width int) string {
	if m.confirm && m.selected >= 0 && m.selected < len(m.config.Sessions) {
		question := lipgloss.NewStyle().Foreground(errorColor).Bold(true).
			Render(truncate("Удалить «"+m.config.Sessions[m.selected].Name+"»?", max(10, width-34)))
		return question + "   " + renderHints([2]string{"Enter", "Удалить"}, [2]string{"Esc", "Отмена"})
	}
	hints := [][2]string{}
	if len(rows) > 0 {
		hints = append(hints, [2]string{"Enter", "Подключиться"})
	}
	if m.launcherQuery() != "" {
		hints = append(hints, [2]string{"Esc", "Очистить"})
	} else {
		hints = append(hints, [2]string{"Ctrl+N", "Новый сервер"})
	}
	hints = append(hints, [2]string{"Ctrl+K", "Все команды"})
	return hintsView(width, hints, hints[len(hints)-1:])
}

func styleSearch(input *textinput.Model, width int) {
	styles := textinput.DefaultDarkStyles()
	styles.Focused.Text = lipgloss.NewStyle().Foreground(textColor)
	styles.Focused.Placeholder = lipgloss.NewStyle().Foreground(muted)
	styles.Blurred.Text = lipgloss.NewStyle().Foreground(textColor)
	styles.Blurred.Placeholder = lipgloss.NewStyle().Foreground(muted)
	styles.Cursor.Color = accentAlt
	input.SetStyles(styles)
	input.SetWidth(max(4, width))
}

// friendlyConnectError turns common network failures into plain language;
// anything unrecognised keeps the original text.
func friendlyConnectError(err error, profile config.Session) string {
	text := err.Error()
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "no such host"):
		return "сервер «" + profile.Host + "» не найден — проверьте адрес"
	case strings.Contains(lower, "connection refused"):
		return fmt.Sprintf("%s не принимает подключения на порту %d", profile.Host, profile.Port)
	case strings.Contains(lower, "i/o timeout") || strings.Contains(lower, "deadline exceeded"):
		return profile.Host + " не отвечает — сервер выключен или недоступен из этой сети"
	case strings.Contains(lower, "network is unreachable") || strings.Contains(lower, "no route to host"):
		return "нет сети до " + profile.Host
	}
	return text
}

// ── Small helpers ───────────────────────────────────────────────────────

func hostWithPort(session config.Session) string {
	return session.Host + portSuffix(session.Port)
}

func portSuffix(port int) string {
	if port == 0 || port == 22 {
		return ""
	}
	return fmt.Sprintf(":%d", port)
}

func padRight(text string, width int) string {
	return text + strings.Repeat(" ", max(0, width-lipgloss.Width(text)))
}

func centerText(text string, width int) string {
	gap := max(0, width-lipgloss.Width(text))
	return strings.Repeat(" ", gap/2) + text + strings.Repeat(" ", gap-gap/2)
}

func padLines(lines []string, height int) []string {
	for len(lines) < height {
		lines = append(lines, "")
	}
	return lines[:height]
}

// ruPlural picks the Russian plural form: 1 сервер, 2 сервера, 5 серверов.
func ruPlural(n int, one, few, many string) string {
	n %= 100
	if n >= 11 && n <= 14 {
		return many
	}
	switch n % 10 {
	case 1:
		return one
	case 2, 3, 4:
		return few
	}
	return many
}

var ruMonths = [...]string{"янв", "фев", "мар", "апр", "мая", "июн", "июл", "авг", "сен", "окт", "ноя", "дек"}

func relativeTime(moment, now time.Time) string {
	elapsed := now.Sub(moment)
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	switch {
	case elapsed < time.Minute:
		return "только что"
	case elapsed < time.Hour:
		n := int(elapsed.Minutes())
		return fmt.Sprintf("%d %s назад", n, ruPlural(n, "минуту", "минуты", "минут"))
	case !moment.Before(startOfToday):
		n := int(elapsed.Hours())
		return fmt.Sprintf("%d %s назад", n, ruPlural(n, "час", "часа", "часов"))
	case !moment.Before(startOfToday.AddDate(0, 0, -1)):
		return "вчера"
	case !moment.Before(startOfToday.AddDate(0, 0, -6)):
		n := int(startOfToday.Sub(moment).Hours()/24) + 1
		return fmt.Sprintf("%d %s назад", n, ruPlural(n, "день", "дня", "дней"))
	case moment.Year() == now.Year():
		return fmt.Sprintf("%d %s", moment.Day(), ruMonths[moment.Month()-1])
	}
	return fmt.Sprintf("%d %s %d", moment.Day(), ruMonths[moment.Month()-1], moment.Year())
}
