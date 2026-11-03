package tui

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"ssh-manager-tui/internal/remote"

	"charm.land/lipgloss/v2"
)

// The application frame: an app bar on top (product name, breadcrumb or tabs,
// version) and a status bar at the bottom (state on the left, the keys for
// the current screen on the right) — the layout of IDEs and admin consoles.
// Screens render only the area in between, see contentHeight.

const appName = "SSH Manager"

func (m *Model) frame(content string) string {
	return m.tabsView() + "\n" + content + "\n" + m.statusBarView()
}

func barStyle() lipgloss.Style {
	return lipgloss.NewStyle().Background(surface).Foreground(muted)
}

func (m *Model) appBrand() string {
	return lipgloss.NewStyle().Background(surface).Foreground(textColor).Bold(true).Render(" " + appName + " ")
}

// tabsView renders the app bar. With one tab it shows a breadcrumb
// ("SSH Manager › Серверы"); with several it lists the tabs.
func (m *Model) tabsView() string {
	bar := barStyle()
	brand := m.appBrand()
	help := m.tabBarHelp()
	line := brand
	if len(m.tabs) <= 1 {
		line += bar.Render("›  " + ellipsize(m.tabTitle(max(0, m.activeTab)), max(1, m.width/2)))
	} else {
		used := lipgloss.Width(brand)
		for index := range m.tabs {
			label := fmt.Sprintf(" %d %s ", index+1, m.tabTitle(index))
			style := bar
			if index == m.activeTab {
				style = lipgloss.NewStyle().Foreground(contrastText).Background(accent).Bold(true)
			}
			rendered := style.Render(label)
			if used+1+lipgloss.Width(rendered) > max(1, m.width-lipgloss.Width(help)-1) {
				break
			}
			line += bar.Render(" ") + rendered
			used += 1 + lipgloss.Width(rendered)
		}
	}
	if gap := m.width - lipgloss.Width(line) - lipgloss.Width(help); gap > 0 {
		line += bar.Render(strings.Repeat(" ", gap)) + help
	}
	return lipgloss.NewStyle().MaxWidth(m.width).Render(line)
}

func (m *Model) tabBarHelp() string {
	clock := barStyle().Foreground(textColor).Render(time.Now().Format("15:04") + "  ")
	if m.appVersion == "" {
		return clock
	}
	return clock + barStyle().Render("sshm "+m.appVersion+" ")
}

// uiTickMsg refreshes time-based readouts (clock, latency, session time)
// while nothing else happens on screen.
type uiTickMsg struct{}

const uiTickInterval = 5 * time.Second

func uiTick() tea.Cmd {
	return tea.Tick(uiTickInterval, func(time.Time) tea.Msg { return uiTickMsg{} })
}

// telemetry is the live readout next to a session name: latency of the last
// keepalive (colour-coded) and how long the session has been up.
func telemetry(conn *remote.Connection) string {
	if conn == nil {
		return ""
	}
	var parts []string
	if rtt := conn.RTT(); rtt > 0 {
		tone := successColor
		switch {
		case rtt >= 250*time.Millisecond:
			tone = errorColor
		case rtt >= 80*time.Millisecond:
			tone = warningColor
		}
		parts = append(parts, lipgloss.NewStyle().Foreground(tone).Render(formatLatency(rtt)))
	}
	if uptime := conn.Uptime(); uptime > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(muted).Render(formatUptime(uptime)))
	}
	return strings.Join(parts, lipgloss.NewStyle().Foreground(borderMuted).Render(" · "))
}

func formatLatency(rtt time.Duration) string {
	if rtt < time.Millisecond {
		return "<1 мс"
	}
	return fmt.Sprintf("%d мс", rtt.Milliseconds())
}

func formatUptime(uptime time.Duration) string {
	minutes := int(uptime.Minutes())
	switch {
	case minutes < 1:
		return "<1 мин"
	case minutes < 60:
		return fmt.Sprintf("%d мин", minutes)
	}
	return fmt.Sprintf("%d ч %02d мин", minutes/60, minutes%60)
}

// withTelemetry right-aligns the readout in a title line of the given width;
// it is dropped when there is no room.
func withTelemetry(title, readout string, width int) string {
	if readout == "" {
		return title
	}
	gap := width - lipgloss.Width(title) - lipgloss.Width(readout) - 1
	if gap < 2 {
		return title
	}
	return title + strings.Repeat(" ", gap) + readout + " "
}

// tabStripOffset is where the first tab starts in the app bar.
func (m *Model) tabStripOffset() int {
	return lipgloss.Width(m.appBrand()) + 1
}

func (m *Model) statusBarView() string {
	bar := barStyle()
	left, leftColor, hints := m.statusBarContent()
	// Keys win over the state text: the state is shortened (down to a
	// readable minimum) before any hint is dropped.
	const minLeft = 24
	right := ""
	compact := compactHintKeys(hints)
	for len(hints) > 0 && right == "" {
		for _, variant := range [][][2]string{hints, compact[:len(hints)]} {
			if rendered := barHints(variant); lipgloss.Width(rendered) <= m.width-minLeft {
				right = rendered
				break
			}
		}
		hints = hints[:len(hints)-1]
	}
	leftText := ""
	if left != "" {
		dot := lipgloss.NewStyle().Background(surface).Foreground(leftColor).Render(" ● ")
		room := max(1, m.width-lipgloss.Width(right)-lipgloss.Width(dot)-2)
		leftText = dot + bar.Foreground(textColor).Render(ellipsize(left, room))
	}
	gap := max(0, m.width-lipgloss.Width(leftText)-lipgloss.Width(right))
	return lipgloss.NewStyle().MaxWidth(m.width).Render(leftText + bar.Render(strings.Repeat(" ", gap)) + right)
}

// barHints renders key hints on the status bar: keycaps on a raised tone,
// labels muted. The padding keeps the " Key " pattern used for mouse clicks.
func barHints(hints [][2]string) string {
	bar := barStyle()
	keycap := lipgloss.NewStyle().Background(borderMuted).Foreground(textColor).Bold(true).Padding(0, 1)
	var out strings.Builder
	for _, hint := range hints {
		out.WriteString(keycap.Render(hint[0]))
		out.WriteString(bar.Render(" " + hint[1] + "  "))
	}
	return out.String()
}

// statusBarContent describes the state of the active screen. Dialogs keep
// their own hints, so the bar then only names where the user is.
func (m *Model) statusBarContent() (string, color.Color, [][2]string) {
	state := func(status string) color.Color {
		switch {
		case strings.HasPrefix(status, "Ошибка") || strings.HasPrefix(status, "Не удалось") || strings.Contains(status, "не прошёл"):
			return errorColor
		case strings.HasPrefix(status, "✓") || strings.HasPrefix(status, "Подключено") || strings.HasPrefix(status, "Скачано") || strings.HasPrefix(status, "Загружено"):
			return successColor
		}
		return muted
	}
	switch {
	case m.width == 0:
		return "", muted, nil
	case m.updateDialog != nil:
		return "Обновление", muted, nil
	case m.hostKeyPrompt != nil:
		return "Проверка ключа сервера", warningColor, nil
	case m.passwordPrompt != nil:
		return "Требуется пароль", warningColor, nil
	case m.palette != nil:
		return "Команды", accentAlt, [][2]string{{"Enter", "Выполнить"}, {"Esc", "Закрыть"}}
	}
	switch m.page {
	case pageSessions:
		if !m.launcherVisible() {
			return m.tabTitle(m.activeTab), muted, nil
		}
		if m.confirm && m.selected >= 0 && m.selected < len(m.config.Sessions) {
			return "Удалить «" + m.config.Sessions[m.selected].Name + "»?", errorColor,
				[][2]string{{"Enter", "Удалить"}, {"Esc", "Отмена"}}
		}
		hints := [][2]string{{"Enter", "Подключиться"}}
		if m.launcherQuery() != "" {
			hints = append(hints, [2]string{"Esc", "Очистить"})
		} else {
			hints = append(hints, [2]string{"Ctrl+N", "Новый сервер"})
		}
		hints = append(hints, [2]string{"Ctrl+K", "Все команды"})
		if m.status != "" {
			return m.status, state(m.status), hints
		}
		return m.launcherSummary(), successColor, hints
	case pageForm:
		return m.tabTitle(m.activeTab), muted, nil
	case pageConnected:
		if m.reconnect != nil {
			return "Переподключение…", warningColor, nil
		}
		if m.forwardModal != nil || m.sftpDialog != nil || m.picker != nil {
			return m.connectedStatus(), state("Подключено"), nil
		}
		if m.filesFocused && !m.sftpHidden {
			return m.connectedStatus(), state(m.statusOrConnected()), m.fileHints()
		}
		return m.connectedStatus(), state(m.statusOrConnected()), m.connectedHints()
	}
	return "", muted, nil
}

func (m *Model) statusOrConnected() string {
	if m.status == "" {
		return "Подключено"
	}
	return m.status
}

func (m *Model) connectedStatus() string {
	return m.statusOrConnected() + " · " + m.profile.User + "@" + hostWithPort(m.profile)
}

func (m *Model) launcherSummary() string {
	saved := len(m.config.Sessions)
	if saved == 0 {
		return "Готово к работе"
	}
	summary := fmt.Sprintf("%d %s", saved, ruPlural(saved, "сервер", "сервера", "серверов"))
	unsaved := 0
	for _, row := range m.browseRowsForSummary() {
		if row.kind == launchHistory {
			unsaved++
		}
	}
	if unsaved > 0 {
		summary += fmt.Sprintf(" · %d в истории", unsaved)
	}
	return summary
}

func (m *Model) browseRowsForSummary() []launchRow {
	if m.launcherQuery() != "" {
		return nil
	}
	return m.launchRows()
}

// fileHints are the status bar keys while the file panel has focus.
func (m *Model) fileHints() [][2]string {
	keys := currentPlatformKeys()
	if m.transferring {
		return [][2]string{{keys.cancel, "Отменить передачу"}, {"Esc k", "Команды"}}
	}
	return [][2]string{
		{keys.download, "Скачать"}, {keys.upload, "Загрузить"}, {"F5", "Обновить"},
		{"Del", "Удалить"}, {"Esc k", "Команды"}, {keys.focus, "Терминал"},
	}
}

// compactHintKeys swaps "Ctrl+D" style labels for the short "^D" form that
// the mouse handler also understands, to keep more hints on narrow screens.
func compactHintKeys(hints [][2]string) [][2]string {
	full, short := currentPlatformKeys(), compactPlatformKeys()
	pairs := map[string]string{
		full.newTab: short.newTab, full.closeTab: short.closeTab, full.toggleSFTP: short.toggleSFTP,
		full.cancel: short.cancel, full.focus: short.focus, full.download: short.download,
		full.upload: short.upload, full.copy: short.copy, full.ports: short.ports, full.tabs: short.tabs,
	}
	result := make([][2]string, len(hints))
	for index, hint := range hints {
		if key, ok := pairs[hint[0]]; ok {
			hint[0] = key
		}
		result[index] = hint
	}
	return result
}
