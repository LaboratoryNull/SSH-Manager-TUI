package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"ssh-manager-tui/internal/config"
	"ssh-manager-tui/internal/remote"
)

type reconnectState struct {
	generation uint64
	attempt    int
	cause      string
	lastError  string
	connecting bool
	retryDelay time.Duration
	cancel     context.CancelFunc
}

type reconnectTickMsg struct {
	tabID      uint64
	generation uint64
	attempt    int
}

type reconnectResultMsg struct {
	tabID      uint64
	generation uint64
	attempt    int
	conn       *remote.Connection
	err        error
}

func (m *Model) beginReconnect(cause error) tea.Cmd {
	if m.reconnect != nil || m.page != pageConnected {
		return nil
	}
	if m.transferCancel != nil {
		m.transferCancel()
	}
	m.clearTransferProgress()
	if m.conn != nil {
		m.conn.Close()
	}
	m.conn = nil
	m.picker, m.sftpDialog, m.sftpBusy = nil, nil, false
	m.sftpOperationID++
	m.forwardModal, m.forwards = nil, nil
	m.escapePending = false
	m.selection, m.clipboardPaste, m.rightPasteDown = nil, false, false
	m.nextReconnectID++
	m.reconnect = &reconnectState{generation: m.nextReconnectID, cause: cause.Error()}
	m.status = "SSH-соединение потеряно"
	if m.term != nil {
		m.term.endSession()
		m.term.Feed([]byte("\r\n\x1b[33m── SSH-соединение потеряно, переподключение… ──\x1b[0m\r\n"))
	}
	return m.startReconnectAttempt()
}

func (m *Model) startReconnectAttempt() tea.Cmd {
	state := m.reconnect
	if state == nil || state.connecting {
		return nil
	}
	state.attempt++
	state.connecting = true
	state.retryDelay = 0
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	state.cancel = cancel
	width, height := m.consoleSize()
	return reconnectCommand(ctx, m.tabID, state.generation, state.attempt, m.profile, width, height)
}

func reconnectCommand(ctx context.Context, tabID, generation uint64, attempt int, profile config.Session, width, height int) tea.Cmd {
	return func() tea.Msg {
		conn, err := remote.Connect(ctx, profile, width, height, false)
		return reconnectResultMsg{
			tabID: tabID, generation: generation, attempt: attempt, conn: conn, err: err,
		}
	}
}

func (m *Model) handleReconnectTick(msg reconnectTickMsg) tea.Cmd {
	state := m.reconnect
	if state == nil || state.generation != msg.generation || state.attempt != msg.attempt || state.connecting {
		return nil
	}
	return m.startReconnectAttempt()
}

func (m *Model) handleReconnectResult(msg reconnectResultMsg) tea.Cmd {
	state := m.reconnect
	if state == nil || state.generation != msg.generation || state.attempt != msg.attempt {
		if msg.conn != nil {
			msg.conn.Close()
		}
		return nil
	}
	if state.cancel != nil {
		state.cancel()
		state.cancel = nil
	}
	state.connecting = false
	if msg.err != nil || msg.conn == nil {
		if msg.err == nil {
			msg.err = fmt.Errorf("SSH-клиент не создал соединение")
		}
		state.lastError = msg.err.Error()
		state.retryDelay = reconnectDelay(state.attempt)
		delay, tabID, generation, attempt := state.retryDelay, m.tabID, state.generation, state.attempt
		return tea.Tick(delay, func(time.Time) tea.Msg {
			return reconnectTickMsg{tabID: tabID, generation: generation, attempt: attempt}
		})
	}

	m.conn = msg.conn
	m.reconnect = nil
	m.status = "SSH-соединение восстановлено"
	m.forwardModal = nil
	m.forwards = make(map[string]*forwardRuntime)
	m.resizeRemote()
	if m.term != nil {
		m.term.ScrollToBottom()
		m.term.Feed([]byte("\r\n\x1b[32m── SSH-соединение восстановлено ──\x1b[0m\r\n"))
		m.term.SetCurrentDirectory(msg.conn.Home)
	}
	m.root = &fileNode{Name: msg.conn.Home, Path: msg.conn.Home, Dir: true, Expanded: true, Loading: true}
	m.visible = []*fileNode{m.root}
	m.fileCursor, m.fileOffset = 0, 0
	commands := []tea.Cmd{waitShellOutput(msg.conn), loadDir(msg.conn.SFTP, m.root)}
	for _, forward := range m.profile.PortForwards {
		if forward.AutoStart {
			commands = append(commands, m.startForward(forward))
		}
	}
	return tea.Batch(commands...)
}

func reconnectDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Second << min(attempt-1, 3)
	return min(delay, 10*time.Second)
}

func (m *Model) handleReconnectKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	state := m.reconnect
	if state == nil {
		return m, nil
	}
	switch key.Keystroke() {
	case "esc", "q":
		if state.cancel != nil {
			state.cancel()
		}
		m.disconnect("Переподключение отменено")
		return m, nil
	case "enter":
		if !state.connecting {
			return m, m.startReconnectAttempt()
		}
	}
	return m, nil
}

func (m *Model) reconnectView() string {
	state := m.reconnect
	if state == nil {
		return m.connectedView()
	}
	boxWidth := min(88, max(44, m.width-6))
	innerWidth := max(18, boxWidth-6)
	phase := fmt.Sprintf("Попытка %d: подключение…", state.attempt)
	if !state.connecting {
		phase = fmt.Sprintf("Попытка %d не удалась • повтор через %s", state.attempt, shortDuration(state.retryDelay))
	}
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(warningColor).Render("⚠ SSH-соединение потеряно"),
		"",
		fieldLine("Хост", m.profile.User+"@"+m.profile.Host, innerWidth),
		lipgloss.NewStyle().Foreground(muted).Render(truncate("Причина: "+state.cause, innerWidth)),
		"",
		lipgloss.NewStyle().Foreground(accentAlt).Bold(true).Render(truncate(phase, innerWidth)),
	}
	if state.lastError != "" {
		lines = append(lines,
			lipgloss.NewStyle().Foreground(errorColor).Render(truncate("Последняя ошибка: "+state.lastError, innerWidth)),
		)
	}
	help := hintsView(innerWidth,
		[][2]string{{"Enter", "повторить сейчас"}, {"Esc", "Отмена"}},
		[][2]string{{"Esc", "Отмена"}},
	)
	if state.connecting {
		help = hintsView(innerWidth, [][2]string{{"Esc", "Отмена"}})
	}
	lines = append(lines,
		"",
		lipgloss.NewStyle().Foreground(muted).Render(truncate("Попытки продолжатся автоматически, пока вы не отмените переподключение.", innerWidth)),
		"",
		help,
	)
	box := borderStyle.Copy().BorderForeground(warningColor).Padding(1, 2).Width(boxWidth - 2).
		Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, m.contentHeight(), lipgloss.Center, lipgloss.Center, box)
}

func shortDuration(value time.Duration) string {
	if value <= 0 {
		return "сейчас"
	}
	return fmt.Sprintf("%d с", max(1, int(value.Round(time.Second)/time.Second)))
}
