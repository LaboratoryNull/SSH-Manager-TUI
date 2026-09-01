package tui

import (
	"context"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	systemclipboard "golang.design/x/clipboard"
)

type clipboardWriteResultMsg struct {
	tabID uint64
	text  string
	err   error
}

type clipboardReadResultMsg struct {
	tabID   uint64
	content string
	err     error
}

var (
	clipboardInitOnce sync.Once
	clipboardInitErr  error
)

func initSystemClipboard() error {
	clipboardInitOnce.Do(func() {
		clipboardInitErr = systemclipboard.Init()
	})
	return clipboardInitErr
}

func writeSystemClipboard(tabID uint64, text string) tea.Cmd {
	return func() tea.Msg {
		if err := initSystemClipboard(); err != nil {
			return clipboardWriteResultMsg{tabID: tabID, text: text, err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, err := systemclipboard.Write(ctx, systemclipboard.FmtText, []byte(text))
		return clipboardWriteResultMsg{tabID: tabID, text: text, err: err}
	}
}

func readSystemClipboard(tabID uint64) tea.Cmd {
	return func() tea.Msg {
		if err := initSystemClipboard(); err != nil {
			return clipboardReadResultMsg{tabID: tabID, err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		content, err := systemclipboard.Read(ctx, systemclipboard.FmtText)
		return clipboardReadResultMsg{tabID: tabID, content: string(content), err: err}
	}
}

func (m *Model) beginClipboardCopy(text string) tea.Cmd {
	m.status = "Копирование текста в системный буфер…"
	return writeSystemClipboard(m.tabID, text)
}

func (m *Model) requestClipboardPaste() tea.Cmd {
	m.clipboardPaste = true
	return readSystemClipboard(m.tabID)
}

func (m *Model) handleClipboardWriteResult(msg clipboardWriteResultMsg) (tea.Model, tea.Cmd) {
	if msg.err == nil {
		m.status = "Текст скопирован в системный буфер"
		return m, nil
	}
	// OSC 52 remains useful in SSH/tmux setups where the application cannot
	// reach the desktop clipboard directly. It has no acknowledgement, so the
	// status deliberately does not claim that copying succeeded.
	m.status = "Системный буфер недоступен напрямую; отправлена резервная команда OSC 52"
	return m, tea.SetClipboard(msg.text)
}

func (m *Model) handleClipboardReadResult(msg clipboardReadResultMsg) (tea.Model, tea.Cmd) {
	if !m.clipboardPaste {
		return m, nil
	}
	if msg.err != nil {
		m.status = "Прямое чтение буфера недоступно; запрос через OSC 52…"
		return m, func() tea.Msg { return tea.ReadClipboard() }
	}
	m.clipboardPaste = false
	if m.page != pageConnected || m.filesFocused {
		return m, nil
	}
	if msg.content == "" {
		m.status = "Буфер обмена пуст"
		return m, nil
	}
	return m.handleConnectedPaste(msg.content)
}
