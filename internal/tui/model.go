package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"ssh-manager-tui/internal/config"
	"ssh-manager-tui/internal/remote"
)

type page int

const (
	pageSessions page = iota
	pageForm
	pageConnected
)

type connectMsg struct {
	conn    *remote.Connection
	profile config.Session
	err     error
	tabID   uint64
}

type keyTestMsg struct {
	err     error
	tabID   uint64
	profile config.Session
}
type shellOutputMsg struct {
	data   []byte
	ok     bool
	source *remote.Connection
}
type escapeTimeoutMsg struct {
	id    uint64
	tabID uint64
}

type tabState struct {
	id          uint64
	page        page
	status      string
	selected    int
	confirm     bool
	groupInput  *textinput.Model
	groupCursor int
	inputs      []textinput.Model
	formFocus   int
	testing     bool
	connecting  bool
	profile     config.Session
	conn        *remote.Connection
	term        *terminalScreen

	filesFocused  bool
	sftpHidden    bool
	root          *fileNode
	visible       []*fileNode
	fileCursor    int
	fileOffset    int
	picker        *localPicker
	escapePending bool
	escapeID      uint64

	transferring         bool
	transferCh           <-chan transferProgressMsg
	transferDone         int64
	transferTotal        int64
	transferPath         string
	transferErr          error
	transferUpload       bool
	transferCancel       context.CancelFunc
	transferRemoteTarget *fileNode
	forwardModal         *forwardModal
	forwards             map[string]*forwardRuntime
	hostKeyPrompt        *hostKeyPrompt
}

type Model struct {
	store         *config.Store
	config        config.Config
	tabs          []*tabState
	activeTab     int
	nextTabID     uint64
	nextForwardID uint64
	tabID         uint64
	page          page
	width         int
	height        int
	status        string
	selected      int
	confirm       bool
	groupInput    *textinput.Model
	groupCursor   int

	inputs     []textinput.Model
	formFocus  int
	testing    bool
	connecting bool

	profile config.Session
	conn    *remote.Connection
	term    *terminalScreen

	filesFocused  bool
	sftpHidden    bool
	root          *fileNode
	visible       []*fileNode
	fileCursor    int
	fileOffset    int
	picker        *localPicker
	escapePending bool
	escapeID      uint64

	transferring         bool
	transferCh           <-chan transferProgressMsg
	transferDone         int64
	transferTotal        int64
	transferPath         string
	transferErr          error
	transferUpload       bool
	transferCancel       context.CancelFunc
	transferRemoteTarget *fileNode
	forwardModal         *forwardModal
	forwards             map[string]*forwardRuntime
	hostKeyPrompt        *hostKeyPrompt
}

var (
	accent        = lipgloss.Color("#7D56F4")
	accentAlt     = lipgloss.Color("#5AF7DC")
	accentWarm    = lipgloss.Color("#FF75B7")
	textColor     = lipgloss.Color("#CDD6F4")
	muted         = lipgloss.Color("#7F849C")
	surface       = lipgloss.Color("#25233A")
	borderMuted   = lipgloss.Color("#45475A")
	errorColor    = lipgloss.Color("#F38BA8")
	successColor  = lipgloss.Color("#A6E3A1")
	warningColor  = lipgloss.Color("#F9E2AF")
	borderStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(borderMuted)
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(accentWarm)
	subtitleStyle = lipgloss.NewStyle().Foreground(accentAlt)
	selectedStyle = lipgloss.NewStyle().Background(surface).Foreground(textColor).Bold(true)
	keyStyle      = lipgloss.NewStyle().Foreground(accentAlt).Background(surface).Padding(0, 1).Bold(true)
	sizeStyle     = lipgloss.NewStyle().Foreground(warningColor).Background(surface)
)

func New(store *config.Store) (*Model, error) {
	cfg, err := store.Load()
	if err != nil {
		return nil, err
	}
	m := &Model{store: store, config: cfg, page: pageSessions, filesFocused: false, sftpHidden: true}
	m.nextTabID, m.tabID = 1, 1
	m.resetForm()
	if order := m.sessionOrder(); len(order) > 0 {
		m.selected = order[0]
	}
	m.tabs = []*tabState{m.captureTab()}
	return m, nil
}

func (m *Model) Init() tea.Cmd { return textinput.Blink }

func (m *Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if index, targeted := m.messageTabIndex(message); targeted {
		if index < 0 {
			switch msg := message.(type) {
			case connectMsg:
				if msg.conn != nil {
					msg.conn.Close()
				}
			case forwardStartedMsg:
				if msg.forward != nil {
					msg.forward.Close()
				}
			}
			return m, nil
		}
		if index != m.activeTab {
			return m.updateInactiveTab(index, message)
		}
	}
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.page == pageConnected {
			m.resizeRemote()
		}
		return m, nil
	case connectMsg:
		m.connecting = false
		if msg.err != nil {
			var mismatch *remote.HostKeyMismatchError
			if errors.As(msg.err, &mismatch) {
				m.hostKeyPrompt = &hostKeyPrompt{change: mismatch, profile: msg.profile}
				m.status = ""
				return m, nil
			}
			m.status = "Ошибка подключения: " + msg.err.Error()
			return m, nil
		}
		m.profile, m.conn, m.page = msg.profile, msg.conn, pageConnected
		m.filesFocused, m.sftpHidden = false, true
		m.forwardModal = nil
		m.forwards = make(map[string]*forwardRuntime)
		m.status = "Подключено"
		m.resizeRemote()
		m.term.SetCurrentDirectory(msg.conn.Home)
		if err := m.recordConnection(msg.profile); err != nil {
			m.status = "Подключено, но статистика не сохранена: " + err.Error()
		}
		m.root = &fileNode{Name: msg.conn.Home, Path: msg.conn.Home, Dir: true, Expanded: true, Loading: true}
		m.visible = []*fileNode{m.root}
		commands := []tea.Cmd{waitShellOutput(m.conn), loadDir(m.conn.SFTP, m.root)}
		for _, forward := range m.profile.PortForwards {
			if forward.AutoStart {
				commands = append(commands, m.startForward(forward))
			}
		}
		return m, tea.Batch(commands...)
	case keyTestMsg:
		m.testing = false
		if msg.err != nil {
			var mismatch *remote.HostKeyMismatchError
			if errors.As(msg.err, &mismatch) {
				m.hostKeyPrompt = &hostKeyPrompt{change: mismatch, profile: msg.profile, keyTest: true}
				m.status = ""
				return m, nil
			}
			m.status = "Ключ не прошёл проверку: " + msg.err.Error()
		} else {
			m.status = "✓ Авторизация публичным ключом работает"
		}
		return m, nil
	case shellOutputMsg:
		if m.page != pageConnected || m.conn != msg.source {
			return m, nil
		}
		if !msg.ok {
			m.disconnect("SSH-сессия завершена")
			return m, nil
		}
		m.term.Feed(msg.data)
		for _, response := range m.term.DrainResponses() {
			if err := m.conn.Send(response); err != nil {
				m.status = "Ошибка ответа терминала: " + err.Error()
				break
			}
		}
		return m, waitShellOutput(m.conn)
	case dirLoadedMsg:
		if m.page != pageConnected || !treeContains(m.root, msg.node) {
			return m, nil
		}
		if msg.loadID != msg.node.loadID {
			return m, nil
		}
		msg.node.Loading = false
		if msg.err != nil {
			m.status = "SFTP: " + msg.err.Error()
			return m, nil
		}
		msg.node.Children, msg.node.Loaded = mergeFileNodes(msg.node.Children, msg.children), true
		m.refreshFiles()
		return m, nil
	case localDirLoadedMsg:
		if m.picker == nil || m.picker.path != msg.path {
			return m, nil
		}
		m.picker.loading = false
		m.picker.err = msg.err
		if msg.err == nil {
			m.picker.entries = msg.entries
			m.picker.cursor, m.picker.offset = 0, 0
		}
		return m, nil
	case escapeTimeoutMsg:
		if !m.escapePending || msg.id != m.escapeID || m.page != pageConnected {
			return m, nil
		}
		m.escapePending = false
		m.status = "Подключено"
		if !m.filesFocused && m.conn != nil {
			if err := m.conn.Send([]byte{0x1b}); err != nil {
				m.status = "Ошибка записи в SSH: " + err.Error()
			}
		}
		return m, nil
	case transferProgressMsg:
		if m.transferCh == nil || msg.source != m.transferCh {
			return m, nil
		}
		m.transferDone, m.transferTotal, m.transferPath, m.transferErr = msg.done, msg.total, msg.path, msg.err
		m.transferUpload = msg.uploading
		if msg.final {
			m.transferring = false
			m.transferCh = nil
			m.transferCancel = nil
			refreshTarget := m.transferRemoteTarget
			m.transferRemoteTarget = nil
			if errors.Is(msg.err, context.Canceled) {
				m.transferErr = nil
				m.transferPath = ""
				m.status = "Передача файлов отменена"
			} else if msg.err != nil {
				operation := "скачивания"
				if msg.uploading {
					operation = "загрузки"
				}
				m.status = "Ошибка " + operation + ": " + msg.err.Error()
			} else if msg.uploading {
				m.status = "Загружено на сервер: " + msg.path
			} else {
				m.status = "Скачано: " + msg.path
			}
			if msg.uploading && refreshTarget != nil && treeContains(m.root, refreshTarget) {
				refreshTarget.Loaded, refreshTarget.Loading, refreshTarget.Expanded = false, true, true
				return m, loadDir(m.conn.SFTP, refreshTarget)
			}
			return m, nil
		}
		return m, waitTransfer(m.transferCh)
	case forwardStartedMsg:
		return m.handleForwardStarted(msg)
	case forwardErrorMsg:
		return m.handleForwardError(msg)
	case tea.KeyPressMsg:
		if model, cmd, handled := m.handleTabKey(msg); handled {
			return model, cmd
		}
		return m.handleKey(msg)
	case tea.PasteMsg:
		if m.page == pageConnected {
			return m.handleConnectedPaste(msg.Content)
		}
	case tea.MouseClickMsg:
		return m.handleMouseClick(msg)
	}

	if m.groupInput != nil {
		var cmd tea.Cmd
		*m.groupInput, cmd = m.groupInput.Update(message)
		return m, cmd
	}
	if m.page == pageForm && m.formFocus < len(m.inputs) {
		var cmd tea.Cmd
		m.inputs[m.formFocus], cmd = m.inputs[m.formFocus].Update(message)
		return m, cmd
	}
	return m, nil
}

func (m *Model) handleKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key.String() == "ctrl+q" {
		m.close()
		return m, tea.Quit
	}
	if m.hostKeyPrompt != nil {
		return m.handleHostKeyPrompt(key)
	}
	if m.groupInput != nil {
		return m.handleGroupKey(key)
	}
	switch m.page {
	case pageSessions:
		return m.handleSessionKey(key)
	case pageForm:
		return m.handleFormKey(key)
	case pageConnected:
		return m.handleConnectedKey(key)
	}
	return m, nil
}

func (m *Model) handleTabKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	s := key.Keystroke()
	switch s {
	case "ctrl+t":
		m.openTab()
		return m, nil, true
	case "ctrl+w":
		m.closeActiveTab()
		return m, nil, true
	case "alt+left":
		m.switchTab((m.activeTab - 1 + len(m.tabs)) % len(m.tabs))
		return m, nil, true
	case "alt+right":
		m.switchTab((m.activeTab + 1) % len(m.tabs))
		return m, nil, true
	case "ctrl+b":
		if m.page == pageConnected {
			m.sftpHidden = !m.sftpHidden
			if m.sftpHidden {
				m.filesFocused = false
			} else {
				m.filesFocused = true
			}
			m.resizeRemote()
		}
		return m, nil, true
	case "alt+p":
		if m.page == pageConnected {
			if m.forwardModal == nil {
				m.forwardModal = &forwardModal{}
			} else {
				m.forwardModal = nil
			}
		}
		return m, nil, true
	}
	if strings.HasPrefix(s, "alt+") && len(s) == len("alt+1") {
		number := int(s[len(s)-1] - '0')
		if number >= 1 && number <= 9 && number <= len(m.tabs) {
			m.switchTab(number - 1)
		}
		return m, nil, true
	}
	return m, nil, false
}

func (m *Model) handleSessionKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if len(m.config.Sessions) == 0 {
		m.selected = 0
		m.confirm = false
	} else if m.selected < 0 || m.selected >= len(m.config.Sessions) {
		m.selected = m.sessionOrder()[0]
		m.confirm = false
	}
	if m.connecting {
		if key.String() == "esc" {
			m.status = "Ожидание сетевого подключения нельзя прервать мгновенно"
		}
		return m, nil
	}
	if m.confirm {
		switch key.String() {
		case "y", "Y", "д", "Д":
			m.config.Sessions = append(m.config.Sessions[:m.selected], m.config.Sessions[m.selected+1:]...)
			if order := m.sessionOrder(); len(order) > 0 {
				m.selected = order[min(m.selected, len(order)-1)]
			} else {
				m.selected = 0
			}
			m.confirm = false
			if err := m.store.Save(m.config); err != nil {
				m.status = "Не удалось сохранить: " + err.Error()
			} else {
				m.status = "Профиль удалён"
			}
		case "n", "N", "н", "Н", "esc":
			m.confirm = false
			m.status = "Удаление отменено"
		}
		return m, nil
	}
	switch key.String() {
	case "ctrl+c":
		m.close()
		return m, tea.Quit
	case "q":
		if len(m.tabs) > 1 {
			m.closeActiveTab()
			return m, nil
		}
		m.close()
		return m, tea.Quit
	case "up", "k":
		m.moveSessionSelection(-1)
	case "down", "j":
		m.moveSessionSelection(1)
	case "n":
		m.resetForm()
		m.page, m.status = pageForm, ""
	case "d":
		if len(m.config.Sessions) > 0 {
			m.confirm = true
			m.status = "Удалить профиль? y/n"
		}
	case "g":
		m.beginGroupEdit()
	case "enter":
		if len(m.config.Sessions) > 0 {
			profile := m.config.Sessions[m.selected]
			m.connecting = true
			m.status = "Подключение к " + profile.Host + "…"
			width, height := m.consoleSize()
			return m, connectCommand(m.tabID, profile, width, height)
		}
	}
	return m, nil
}

func (m *Model) handleFormKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.testing || m.connecting {
		return m, nil
	}
	count := len(m.inputs) + 3
	switch key.String() {
	case "esc":
		m.page = pageSessions
		m.blurInputs()
		return m, nil
	case "tab", "down":
		m.setFormFocus((m.formFocus + 1) % count)
		return m, nil
	case "shift+tab", "up":
		m.setFormFocus((m.formFocus - 1 + count) % count)
		return m, nil
	case "enter":
		if m.formFocus < len(m.inputs) {
			m.setFormFocus(m.formFocus + 1)
			return m, nil
		}
		profile, err := m.formProfile()
		if err != nil {
			m.status = err.Error()
			return m, nil
		}
		switch m.formFocus - len(m.inputs) {
		case -1, 0:
			m.upsert(profile)
			if err := m.store.Save(m.config); err != nil {
				m.status = "Не удалось сохранить: " + err.Error()
				return m, nil
			}
			m.connecting = true
			m.status = "Профиль сохранён, подключение…"
			width, height := m.consoleSize()
			return m, connectCommand(m.tabID, profile, width, height)
		case 1:
			m.testing = true
			m.status = "Проверка ключа…"
			return m, testKeyCommand(m.tabID, profile)
		case 2:
			m.page = pageSessions
			m.blurInputs()
		}
		return m, nil
	}
	if m.formFocus < len(m.inputs) {
		var cmd tea.Cmd
		m.inputs[m.formFocus], cmd = m.inputs[m.formFocus].Update(key)
		return m, cmd
	}
	return m, nil
}

func (m *Model) handleConnectedKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := key.Keystroke()
	if m.forwardModal != nil {
		return m.handleForwardKey(key)
	}
	if m.picker != nil {
		return m.handlePickerKey(key)
	}
	if m.transferring && s == "ctrl+x" {
		m.cancelTransfer()
		return m, nil
	}
	if s == "ctrl+shift+c" && !m.filesFocused {
		if m.term == nil || strings.TrimSpace(m.term.String()) == "" {
			m.status = "В терминале пока нет текста для копирования"
			return m, nil
		}
		m.status = "Текст видимой области терминала скопирован"
		return m, tea.SetClipboard(strings.TrimRight(m.term.String(), "\n"))
	}
	if m.escapePending {
		m.escapePending = false
		if s == "q" {
			m.disconnect("Отключено")
			return m, nil
		}
		if !m.filesFocused && m.conn != nil {
			if err := m.conn.Send([]byte{0x1b}); err != nil {
				m.status = "Ошибка записи в SSH: " + err.Error()
			}
		}
	}
	if s == "esc" {
		m.escapePending = true
		m.escapeID++
		m.status = "Для выхода нажмите q после Esc"
		return m, escapeTimeoutCommand(m.tabID, m.escapeID)
	}
	if s == "ctrl+space" || s == "ctrl+@" {
		if m.sftpHidden {
			return m, nil
		}
		m.filesFocused = !m.filesFocused
		return m, nil
	}
	if m.filesFocused && s == "ctrl+d" {
		return m, m.openDownloadPicker()
	}
	if m.filesFocused && s == "ctrl+u" {
		return m, m.openUploadPicker()
	}
	if m.filesFocused {
		switch s {
		case "up", "k":
			if m.fileCursor > 0 {
				m.fileCursor--
			}
			m.ensureFileVisible()
		case "down", "j":
			if m.fileCursor+1 < len(m.visible) {
				m.fileCursor++
			}
			m.ensureFileVisible()
		case "enter", "right", "l":
			return m, m.expandSelected()
		case "left", "h":
			return m, m.collapseSelected()
		case "backspace":
			return m, m.openRemoteDirectory(path.Dir(m.root.Path))
		case "r", "f5":
			return m, m.reloadSelectedDirectory()
		case "/":
			return m, m.openRemoteDirectory("/")
		case "home":
			return m, m.openRemoteDirectory(m.conn.Home)
		}
		return m, nil
	}
	data := keyBytes(key)
	if len(data) == 0 {
		return m, nil
	}
	if err := m.conn.Send(data); err != nil {
		m.status = "Ошибка записи в SSH: " + err.Error()
	}
	return m, nil
}

func (m *Model) handleConnectedPaste(content string) (tea.Model, tea.Cmd) {
	if m.forwardModal != nil || m.picker != nil || m.filesFocused || m.conn == nil {
		return m, nil
	}
	if localPaths, ok := droppedLocalPaths(content); ok {
		remoteDirectory := m.conn.Home
		if m.term != nil && m.term.CurrentDirectory() != "" {
			remoteDirectory = m.term.CurrentDirectory()
		}
		target := findFileNode(m.root, remoteDirectory)
		if target == nil {
			target = &fileNode{Name: path.Base(remoteDirectory), Path: remoteDirectory, Dir: true}
		}
		return m, m.startUpload(localPaths, target)
	}
	data := []byte(content)
	if m.term != nil {
		data = m.term.PasteBytes(content)
	}
	if err := m.conn.Send(data); err != nil {
		m.status = "Ошибка вставки в SSH: " + err.Error()
	}
	return m, nil
}

func (m *Model) handlePickerKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	picker := m.picker
	if picker == nil {
		return m, nil
	}
	switch key.String() {
	case "esc":
		m.picker = nil
		m.status = "Выбор отменён"
	case "up", "k":
		if picker.cursor > 0 {
			picker.cursor--
		}
		picker.ensureVisible(m.pickerListHeight())
	case "down", "j":
		if picker.cursor+1 < len(picker.entries) {
			picker.cursor++
		}
		picker.ensureVisible(m.pickerListHeight())
	case "left", "backspace":
		return m, picker.parent()
	case "enter":
		if selected := picker.selected(); selected != nil {
			if selected.dir {
				return m, picker.enter(selected.path)
			}
			if picker.mode == pickerUploadSource {
				picker.marked[selected.path] = true
				return m, m.chooseUploadSources()
			}
		}
	case "right", "l":
		if selected := picker.selected(); selected != nil && selected.dir {
			return m, picker.enter(selected.path)
		}
	case "s":
		if picker.mode == pickerDownloadDestination && !picker.loading {
			node, destination := picker.remoteTarget, picker.path
			m.picker = nil
			return m, m.startSelectedDownload(destination, node)
		}
	case "space":
		if picker.mode == pickerDownloadDestination && !picker.loading {
			node, destination := picker.remoteTarget, picker.path
			m.picker = nil
			return m, m.startSelectedDownload(destination, node)
		}
		if picker.mode == pickerUploadSource && !picker.loading {
			if selected := picker.selected(); selected != nil {
				picker.marked[selected.path] = !picker.marked[selected.path]
			}
		}
	case "u":
		if picker.mode == pickerUploadSource {
			return m, m.chooseUploadSources()
		}
	}
	return m, nil
}

func (m *Model) View() tea.View {
	view := tea.NewView(m.viewContent())
	view.AltScreen = true
	view.KeyboardEnhancements.ReportAlternateKeys = true
	view.KeyboardEnhancements.ReportAllKeysAsEscapeCodes = true
	view.KeyboardEnhancements.ReportAssociatedText = true
	view.MouseMode = tea.MouseModeCellMotion
	return view
}

func (m *Model) viewContent() string {
	if m.width == 0 {
		return "Запуск ssh-manager…"
	}
	var content string
	if m.hostKeyPrompt != nil {
		return m.tabsView() + "\n" + m.hostKeyPromptView()
	}
	if m.groupInput != nil {
		return m.tabsView() + "\n" + m.groupPromptView()
	}
	switch m.page {
	case pageForm:
		content = m.formView()
	case pageConnected:
		if m.forwardModal != nil {
			content = m.forwardView()
		} else if m.picker != nil {
			content = m.pickerView()
		} else {
			content = m.connectedView()
		}
	default:
		content = m.sessionsView()
	}
	return m.tabsView() + "\n" + content
}

func (m *Model) tabsView() string {
	parts := make([]string, 0, len(m.tabs)+1)
	used := 0
	help := renderHints([2]string{"^T", "+"}, [2]string{"^W", "×"}, [2]string{"Alt←/→", "вкладки"})
	for index := range m.tabs {
		label := fmt.Sprintf(" %d %s ", index+1, m.tabTitle(index))
		style := lipgloss.NewStyle().Foreground(muted).Background(surface)
		if index == m.activeTab {
			style = lipgloss.NewStyle().Foreground(lipgloss.Color("#11111B")).Background(accent).Bold(true)
		}
		rendered := style.Render(label)
		separatorWidth := 1
		if used+separatorWidth+lipgloss.Width(rendered) > max(1, m.width-lipgloss.Width(help)-1) {
			break
		}
		parts = append(parts, rendered)
		used += separatorWidth + lipgloss.Width(rendered)
	}
	line := strings.Join(parts, " ")
	remaining := m.width - lipgloss.Width(line) - lipgloss.Width(help)
	if remaining > 0 {
		line += strings.Repeat(" ", remaining) + help
	}
	return lipgloss.NewStyle().Width(m.width).MaxWidth(m.width).Render(line)
}

func (m *Model) tabTitle(index int) string {
	state := m.tabs[index]
	currentPage, profile, connecting := state.page, state.profile, state.connecting
	if index == m.activeTab {
		currentPage, profile, connecting = m.page, m.profile, m.connecting
	}
	if connecting {
		return "Подключение…"
	}
	if currentPage == pageConnected {
		if profile.Name != "" {
			return ellipsize(profile.Name, 18)
		}
		return ellipsize(profile.Host, 18)
	}
	if currentPage == pageForm {
		return "Новая сессия"
	}
	return "Выбор хоста"
}

func (m *Model) contentHeight() int {
	return max(1, m.height-1)
}

func (m *Model) connectedHints(width int) string {
	if m.transferring {
		return hintsView(width,
			[][2]string{{"Ctrl+X", "отменить передачу"}, {"Ctrl+B", "SFTP"}, {"Esc q", "отключить"}},
			[][2]string{{"^X", "отмена"}, {"^B", "SFTP"}},
			[][2]string{{"^X", "отмена"}},
		)
	}
	return hintsView(width,
		[][2]string{
			{"Ctrl+T", "новая вкладка"}, {"Ctrl+W", "закрыть"}, {"Alt+←/→", "вкладки"},
			{"Alt+1…9", "по номеру"}, {"Ctrl+B", "SFTP"}, {"Ctrl+Shift+C", "копировать"}, {"Alt+P", "порты"}, {"Esc q", "отключить"},
		},
		[][2]string{
			{"^T", "+вкладка"}, {"^W", "закрыть"}, {"Alt←/→", "вкладки"},
			{"^B", "SFTP"}, {"AltP", "порты"}, {"Esc q", "выход"},
		},
		[][2]string{{"^T", "+"}, {"^W", "×"}, {"^B", "SFTP"}, {"AltP", "порты"}, {"Esc q", "выход"}},
		[][2]string{{"^T", "+"}, {"^W", "×"}, {"^B", "SFTP"}, {"AltP", "порты"}},
		[][2]string{{"^B", "SFTP"}, {"AltP", "порты"}},
	)
}

func (m *Model) sessionsView() string {
	title := titleStyle.Render("◆ SSH") + subtitleStyle.Copy().Bold(true).Render(" MANAGER")
	lines := []string{title, lipgloss.NewStyle().Foreground(muted).Render("Сохранённые подключения"), ""}
	if len(m.config.Sessions) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(muted).Italic(true).Render("Сохранённых сессий пока нет."))
	} else {
		previousGroup := "\x00"
		for _, i := range m.sessionOrder() {
			profile := m.config.Sessions[i]
			group := strings.TrimSpace(profile.Group)
			if group != previousGroup {
				if previousGroup != "\x00" {
					lines = append(lines, "")
				}
				groupLabel := group
				if groupLabel == "" {
					groupLabel = "Без группы"
				}
				lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(accentWarm).Render("▰ "+groupLabel))
				previousGroup = group
			}
			cursor := "  "
			style := lipgloss.NewStyle().Foreground(textColor)
			if i == m.selected {
				cursor = "▸ "
				style = selectedStyle.Copy().Padding(0, 1)
			}
			frequency := ""
			if profile.ConnectionCount > 0 {
				frequency = lipgloss.NewStyle().Foreground(muted).Render(fmt.Sprintf("  ×%d", profile.ConnectionCount))
			}
			line := fmt.Sprintf("%s%s  %s%s", cursor, profile.Name, subtitleStyle.Render(fmt.Sprintf("%s@%s:%d", profile.User, profile.Host, profile.Port)), frequency)
			lines = append(lines, style.Render(line))
		}
	}
	lines = append(lines, "", renderHints(
		[2]string{"↑/↓", "выбор"}, [2]string{"Enter", "подключиться"},
		[2]string{"n", "новая"}, [2]string{"g", "переместить"}, [2]string{"d", "удалить"}, [2]string{"q", "закрыть"},
	))
	if m.status != "" {
		lines = append(lines, "", statusLine(m.status))
	}
	boxWidth := min(max(74, lipgloss.Width(strings.Join(lines, "\n"))+6), max(20, m.width-4))
	box := borderStyle.Copy().Padding(1, 2).Width(boxWidth).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, m.contentHeight(), lipgloss.Center, lipgloss.Center, box)
}

func (m *Model) groupPromptView() string {
	if m.groupInput == nil || m.selected < 0 || m.selected >= len(m.config.Sessions) {
		return m.sessionsView()
	}
	profile := m.config.Sessions[m.selected]
	suggestions := m.groupSuggestions()
	innerWidth := min(68, max(24, m.width-10))
	lines := []string{
		titleStyle.Render("Переместить хост"),
		lipgloss.NewStyle().Foreground(muted).Render(truncate(profile.Name+"  "+profile.User+"@"+profile.Host, innerWidth)),
		"",
		lipgloss.NewStyle().Foreground(accentAlt).Bold(true).Render("Группа"),
		m.groupInput.View(),
		"",
		lipgloss.NewStyle().Foreground(muted).Bold(true).Render("Подсказки"),
	}
	start := 0
	if m.groupCursor >= 5 {
		start = m.groupCursor - 4
	}
	end := min(len(suggestions), start+5)
	for i := start; i < end; i++ {
		prefix := "  "
		style := lipgloss.NewStyle().Foreground(textColor).Width(innerWidth)
		if i == m.groupCursor {
			prefix = "▸ "
			style = selectedStyle.Copy().Foreground(accentAlt).Width(innerWidth)
		}
		lines = append(lines, style.Render(truncate(prefix+suggestions[i], innerWidth)))
	}
	if len(suggestions) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(muted).Italic(true).Render("Нет совпадений — будет создана новая группа"))
	}
	lines = append(lines,
		"",
		lipgloss.NewStyle().Foreground(muted).Render("Пустое поле означает «Без группы»."),
		"",
		renderHints([2]string{"↑/↓", "выбрать"}, [2]string{"Tab", "подставить"}, [2]string{"Enter", "сохранить"}, [2]string{"Esc", "отмена"}),
	)
	box := borderStyle.Copy().BorderForeground(accentWarm).Padding(1, 2).Width(innerWidth + 4).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, m.contentHeight(), lipgloss.Center, lipgloss.Center, box)
}

func (m *Model) formView() string {
	labels := []string{"Название", "Хост", "Порт", "Пользователь", "Пароль", "Приватный ключ", "Группа"}
	lines := []string{titleStyle.Render("◆ Новая SSH-сессия"), subtitleStyle.Render("Параметры подключения"), ""}
	for i := range m.inputs {
		labelStyle := lipgloss.NewStyle().Width(18).Foreground(muted)
		if m.formFocus == i {
			labelStyle = labelStyle.Foreground(accentAlt).Bold(true)
		}
		label := labelStyle.Render(labels[i])
		lines = append(lines, label+m.inputs[i].View())
	}
	lines = append(lines, "")
	buttons := []string{"Сохранить и подключить", "Проверить ключ", "Отмена"}
	var rendered []string
	for i, label := range buttons {
		style := lipgloss.NewStyle().Padding(0, 1).Foreground(muted).Background(surface)
		if m.formFocus == len(m.inputs)+i {
			style = style.Background(accent).Foreground(lipgloss.Color("#11111B")).Bold(true)
		}
		rendered = append(rendered, style.Render(label))
	}
	lines = append(lines, strings.Join(rendered, "  "))
	lines = append(lines, "", renderHints([2]string{"Tab/↑/↓", "переход"}, [2]string{"Enter", "выбрать"}, [2]string{"Esc", "назад"}))
	if m.status != "" {
		lines = append(lines, "", statusLine(m.status))
	}
	box := borderStyle.Copy().Padding(1, 2).Width(min(76, max(30, m.width-6))).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, m.contentHeight(), lipgloss.Center, lipgloss.Center, box)
}

func (m *Model) connectedView() string {
	fileW, consoleW, innerH, termW, _ := m.layout()
	fileBorder := borderMuted
	consoleBorder := borderMuted
	if m.filesFocused {
		fileBorder = accentAlt
	} else {
		consoleBorder = accent
	}
	consoleTitle := fmt.Sprintf(" ● SSH  %s@%s • Esc q: назад ", m.profile.User, m.profile.Host)
	if m.status != "" && m.status != "Подключено" {
		consoleTitle += " • " + m.status
	}
	consoleTitle = truncate(consoleTitle, termW)
	consoleLines := []string{lipgloss.NewStyle().Bold(true).Foreground(successColor).Render(consoleTitle)}
	termText := ""
	if m.term != nil {
		termText = m.term.RenderANSI()
	}
	consoleLines = append(consoleLines, termText, m.connectedHints(termW))
	consoleContent := lipgloss.NewStyle().Width(termW).Height(innerH).MaxHeight(innerH).Render(strings.Join(consoleLines, "\n"))
	right := borderStyle.Copy().BorderForeground(consoleBorder).Width(consoleW).Height(m.contentHeight()).Render(consoleContent)
	if m.sftpHidden {
		return right
	}
	fileContent := m.filesView(fileW-2, innerH)
	left := borderStyle.Copy().BorderForeground(fileBorder).Width(fileW).Height(m.contentHeight()).Render(fileContent)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func (m *Model) filesView(width, height int) string {
	header := lipgloss.NewStyle().Bold(true).Foreground(accentAlt).Render(" ◆ SFTP ")
	footerLines := 1
	if m.transferring || m.transferPath != "" {
		footerLines = 2
	}
	listH := max(1, height-1-footerLines)
	m.ensureFileVisibleHeight(listH)
	lines := []string{header}
	end := min(len(m.visible), m.fileOffset+listH)
	for i := m.fileOffset; i < end; i++ {
		node := m.visible[i]
		prefix := strings.Repeat("  ", node.Depth)
		icon := "· "
		if node.Dir {
			icon = "▸ "
			if node.Expanded {
				icon = "▾ "
			}
			if node.Loading {
				icon = "… "
			}
		}
		line := truncate(prefix+icon+node.Name, width)
		style := lipgloss.NewStyle().Width(width).Foreground(textColor)
		if node.Dir {
			style = style.Foreground(accentAlt)
		}
		if i == m.fileCursor {
			style = selectedStyle.Copy().Width(width).Foreground(accentAlt)
		}
		lines = append(lines, style.Render(line))
	}
	for len(lines) < height-footerLines {
		lines = append(lines, "")
	}
	if m.transferring || m.transferPath != "" {
		lines = append(lines, m.progressView(width))
	}
	var help string
	if m.transferring {
		help = hintsView(width,
			[][2]string{{"Ctrl+X", "отменить передачу"}},
			[][2]string{{"^X", "отмена"}},
		)
	} else {
		help = hintsView(width,
			[][2]string{{"Ctrl+Space", "фокус"}, {"Ctrl+D", "скачать"}, {"Ctrl+U", "загрузить"}, {"F5", "обновить"}, {"/", "корень"}},
			[][2]string{{"^D", "↓"}, {"^U", "↑"}, {"F5", "↻"}},
			[][2]string{{"F5", "обновить"}},
		)
	}
	lines = append(lines, help)
	return strings.Join(lines, "\n")
}

func (m *Model) progressView(width int) string {
	if m.transferErr != nil {
		return lipgloss.NewStyle().Foreground(errorColor).Render(truncate("✗ "+m.transferErr.Error(), width))
	}
	ratio := 0.0
	if m.transferTotal > 0 {
		ratio = float64(m.transferDone) / float64(m.transferTotal)
	} else if !m.transferring {
		ratio = 1
	}
	ratio = min(1.0, max(0.0, ratio))
	barW := max(3, width-8)
	filled := min(barW, int(ratio*float64(barW)))
	bar := lipgloss.NewStyle().Foreground(accent).Render(strings.Repeat("█", filled)) +
		lipgloss.NewStyle().Foreground(borderMuted).Render(strings.Repeat("░", barW-filled))
	direction := "↓"
	if m.transferUpload {
		direction = "↑"
	}
	return subtitleStyle.Copy().Bold(true).Render(direction) + bar + warningColorStyle(fmt.Sprintf(" %3.0f%%", ratio*100))
}

func (m *Model) pickerView() string {
	picker := m.picker
	if picker == nil {
		return m.connectedView()
	}
	boxWidth := min(88, max(34, m.width-6))
	boxHeight := min(28, max(12, m.contentHeight()-4))
	innerWidth := max(10, boxWidth-6)
	title := "Куда сохранить на локальном компьютере"
	help := hintsView(innerWidth,
		[][2]string{{"Enter", "открыть"}, {"←/Backspace", "выше"}, {"s", "сохранить сюда"}, {"Esc", "отмена"}},
		[][2]string{{"Enter", "открыть"}, {"s", "сохранить"}, {"Esc", "отмена"}},
	)
	if picker.mode == pickerUploadSource {
		title = "Выберите локальный файл или каталог"
		help = hintsView(innerWidth,
			[][2]string{{"Enter", "открыть/загрузить"}, {"Space", "отметить"}, {"u", "загрузить"}, {"Esc", "отмена"}},
			[][2]string{{"Space", "отметить"}, {"u", "загрузить"}, {"Esc", "отмена"}},
		)
	}
	lines := []string{
		titleStyle.Render("◆ " + title),
		subtitleStyle.Render("⌂ ") + lipgloss.NewStyle().Foreground(muted).Render(truncate(picker.path, max(1, innerWidth-2))),
		"",
	}
	listHeight := max(1, boxHeight-9)
	picker.ensureVisible(listHeight)
	if picker.loading {
		lines = append(lines, lipgloss.NewStyle().Foreground(muted).Render("Загрузка каталога…"))
	} else if len(picker.entries) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(muted).Render("Каталог пуст"))
	} else {
		end := min(len(picker.entries), picker.offset+listHeight)
		for i := picker.offset; i < end; i++ {
			entry := picker.entries[i]
			icon := "📄 "
			if picker.marked[entry.path] {
				icon = "✓  "
			} else if entry.dir {
				icon = "📁 "
			} else if entry.mode&os.ModeSymlink != 0 {
				icon = "🔗 "
			}
			line := pickerEntryLine(entry, icon, innerWidth)
			style := lipgloss.NewStyle().Width(innerWidth).Foreground(textColor)
			if i == picker.cursor {
				style = selectedStyle.Copy().Width(innerWidth)
			}
			lines = append(lines, style.Render(line))
		}
	}
	for len(lines) < boxHeight-5 {
		lines = append(lines, "")
	}
	if picker.err != nil {
		lines = append(lines, lipgloss.NewStyle().Foreground(errorColor).Render(truncate("Ошибка: "+picker.err.Error(), innerWidth)))
	} else {
		lines = append(lines, "")
	}
	lines = append(lines, help)
	box := borderStyle.Copy().BorderForeground(accentWarm).Padding(1, 2).Width(boxWidth - 2).Height(boxHeight - 2).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, m.contentHeight(), lipgloss.Center, lipgloss.Center, box)
}

func (m *Model) pickerListHeight() int {
	return max(1, min(28, max(12, m.contentHeight()-4))-9)
}

func (m *Model) resetForm() {
	values := []struct{ placeholder, value string }{
		{"например, production", ""}, {"example.com", ""}, {"22", "22"}, {"root", currentUser()},
		{"необязательно", ""}, {"~/.ssh/id_ed25519 (пусто = авто)", ""}, {"например, Production", ""},
	}
	m.inputs = make([]textinput.Model, len(values))
	for i, item := range values {
		input := textinput.New()
		styles := textinput.DefaultDarkStyles()
		styles.Focused.Text = lipgloss.NewStyle().Foreground(textColor)
		styles.Focused.Placeholder = lipgloss.NewStyle().Foreground(borderMuted).Italic(true)
		styles.Blurred.Text = lipgloss.NewStyle().Foreground(muted)
		styles.Blurred.Placeholder = lipgloss.NewStyle().Foreground(borderMuted)
		styles.Cursor.Color = accentWarm
		input.SetStyles(styles)
		input.Prompt = ""
		input.Placeholder = item.placeholder
		input.SetValue(item.value)
		input.SetWidth(45)
		input.CharLimit = 512
		m.inputs[i] = input
	}
	m.inputs[4].EchoMode = textinput.EchoPassword
	m.inputs[4].EchoCharacter = '•'
	m.formFocus = 0
	m.setFormFocus(0)
}

func (m *Model) setFormFocus(index int) {
	m.blurInputs()
	m.formFocus = index
	if index < len(m.inputs) {
		m.inputs[index].Focus()
	}
}

func (m *Model) blurInputs() {
	for i := range m.inputs {
		m.inputs[i].Blur()
	}
}

func (m *Model) formProfile() (config.Session, error) {
	port, err := strconv.Atoi(strings.TrimSpace(m.inputs[2].Value()))
	if err != nil {
		return config.Session{}, fmt.Errorf("порт должен быть числом")
	}
	profile := config.Session{
		Name: strings.TrimSpace(m.inputs[0].Value()), Host: strings.TrimSpace(m.inputs[1].Value()),
		Port: port, User: strings.TrimSpace(m.inputs[3].Value()), Password: m.inputs[4].Value(),
		KeyPath: strings.TrimSpace(m.inputs[5].Value()), Group: strings.TrimSpace(m.inputs[6].Value()),
	}
	if err := profile.Validate(); err != nil {
		return config.Session{}, err
	}
	return profile, nil
}

func (m *Model) upsert(profile config.Session) {
	for i := range m.config.Sessions {
		if m.config.Sessions[i].Name == profile.Name {
			if profile.PortForwards == nil {
				profile.PortForwards = append([]config.PortForward(nil), m.config.Sessions[i].PortForwards...)
			}
			profile.ConnectionCount = m.config.Sessions[i].ConnectionCount
			m.config.Sessions[i] = profile
			m.selected = i
			return
		}
	}
	m.config.Sessions = append(m.config.Sessions, profile)
	m.selected = len(m.config.Sessions) - 1
}

func (m *Model) sessionOrder() []int {
	order := make([]int, len(m.config.Sessions))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		left, right := m.config.Sessions[order[a]], m.config.Sessions[order[b]]
		leftGroup, rightGroup := strings.ToLower(strings.TrimSpace(left.Group)), strings.ToLower(strings.TrimSpace(right.Group))
		if leftGroup != rightGroup {
			if leftGroup == "" {
				return true
			}
			if rightGroup == "" {
				return false
			}
			return leftGroup < rightGroup
		}
		if left.ConnectionCount != right.ConnectionCount {
			return left.ConnectionCount > right.ConnectionCount
		}
		return strings.ToLower(left.Name) < strings.ToLower(right.Name)
	})
	return order
}

func (m *Model) moveSessionSelection(delta int) {
	order := m.sessionOrder()
	if len(order) == 0 {
		m.selected = 0
		return
	}
	position := 0
	for i, index := range order {
		if index == m.selected {
			position = i
			break
		}
	}
	position = clamp(position+delta, 0, len(order)-1)
	m.selected = order[position]
}

func (m *Model) sessionGroups() []string {
	seen := make(map[string]string)
	for _, session := range m.config.Sessions {
		group := strings.TrimSpace(session.Group)
		if group != "" {
			seen[strings.ToLower(group)] = group
		}
	}
	groups := make([]string, 0, len(seen))
	for _, group := range seen {
		groups = append(groups, group)
	}
	sort.Slice(groups, func(i, j int) bool { return strings.ToLower(groups[i]) < strings.ToLower(groups[j]) })
	return groups
}

func (m *Model) groupSuggestions() []string {
	if m.groupInput == nil {
		return nil
	}
	query := strings.ToLower(strings.TrimSpace(m.groupInput.Value()))
	var prefixes, contains []string
	for _, group := range m.sessionGroups() {
		lower := strings.ToLower(group)
		if query == "" || strings.HasPrefix(lower, query) {
			prefixes = append(prefixes, group)
		} else if strings.Contains(lower, query) {
			contains = append(contains, group)
		}
	}
	return append(prefixes, contains...)
}

func (m *Model) beginGroupEdit() {
	if m.selected < 0 || m.selected >= len(m.config.Sessions) {
		return
	}
	input := textinput.New()
	styles := textinput.DefaultDarkStyles()
	styles.Focused.Text = lipgloss.NewStyle().Foreground(textColor)
	styles.Focused.Placeholder = lipgloss.NewStyle().Foreground(borderMuted).Italic(true)
	styles.Cursor.Color = accentWarm
	input.SetStyles(styles)
	input.Prompt = "▸ "
	input.Placeholder = "Без группы"
	input.SetWidth(min(60, max(16, m.width-16)))
	input.CharLimit = 128
	input.SetValue(m.config.Sessions[m.selected].Group)
	input.Focus()
	m.groupInput = &input
	m.groupCursor = -1
}

func (m *Model) handleGroupKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.groupInput == nil {
		return m, nil
	}
	switch key.Keystroke() {
	case "esc":
		m.groupInput = nil
		m.groupCursor = -1
		m.status = "Перемещение отменено"
		return m, nil
	case "down", "ctrl+n":
		suggestions := m.groupSuggestions()
		if len(suggestions) > 0 {
			m.groupCursor = (m.groupCursor + 1) % len(suggestions)
		}
		return m, nil
	case "up", "ctrl+p":
		suggestions := m.groupSuggestions()
		if len(suggestions) > 0 {
			if m.groupCursor < 0 {
				m.groupCursor = len(suggestions) - 1
			} else {
				m.groupCursor = (m.groupCursor - 1 + len(suggestions)) % len(suggestions)
			}
		}
		return m, nil
	case "tab":
		suggestions := m.groupSuggestions()
		if len(suggestions) > 0 {
			index := m.groupCursor
			if index < 0 || index >= len(suggestions) {
				index = 0
			}
			m.groupInput.SetValue(suggestions[index])
			m.groupInput.CursorEnd()
			m.groupCursor = -1
		}
		return m, nil
	case "enter":
		if m.selected < 0 || m.selected >= len(m.config.Sessions) {
			m.groupInput = nil
			return m, nil
		}
		group := strings.TrimSpace(m.groupInput.Value())
		if suggestions := m.groupSuggestions(); m.groupCursor >= 0 && m.groupCursor < len(suggestions) {
			group = suggestions[m.groupCursor]
		}
		if strings.IndexFunc(group, unicode.IsControl) >= 0 {
			m.status = "Название группы не должно содержать управляющие символы"
			return m, nil
		}
		old := m.config.Sessions[m.selected].Group
		m.config.Sessions[m.selected].Group = group
		m.groupInput = nil
		m.groupCursor = -1
		if err := m.store.Save(m.config); err != nil {
			m.config.Sessions[m.selected].Group = old
			m.status = "Не удалось переместить хост: " + err.Error()
		} else if group == "" {
			m.status = "Хост перемещён в раздел «Без группы»"
		} else {
			m.status = "Хост перемещён в группу «" + group + "»"
		}
		return m, nil
	}
	var cmd tea.Cmd
	*m.groupInput, cmd = m.groupInput.Update(key)
	m.groupCursor = -1
	return m, cmd
}

func (m *Model) recordConnection(profile config.Session) error {
	for i := range m.config.Sessions {
		if m.config.Sessions[i].Name != profile.Name {
			continue
		}
		m.config.Sessions[i].ConnectionCount++
		m.profile = m.config.Sessions[i]
		if m.store == nil {
			return nil
		}
		return m.store.Save(m.config)
	}
	return nil
}

func (m *Model) layout() (fileW, consoleW, innerH, termW, termH int) {
	if m.sftpHidden {
		fileW = 0
		consoleW = max(1, m.width)
		innerH = max(3, m.contentHeight()-2)
		termW = max(1, consoleW-2)
		termH = max(1, innerH-2)
		return
	}
	fileW = max(18, m.width/5)
	if fileW > m.width-12 {
		fileW = max(8, m.width/3)
	}
	consoleW = max(10, m.width-fileW)
	innerH = max(3, m.contentHeight()-2)
	termW = max(1, consoleW-2)
	termH = max(1, innerH-2)
	return
}

func (m *Model) consoleSize() (int, int) {
	_, _, _, w, h := m.layout()
	return w, h
}

func (m *Model) resizeRemote() {
	w, h := m.consoleSize()
	if m.term == nil {
		m.term = newTerminalScreen(w, h)
	} else {
		m.term.Resize(w, h)
	}
	if m.conn != nil {
		_ = m.conn.Resize(w, h)
	}
}

func (m *Model) refreshFiles() {
	m.visible = flattenTree(m.root)
	if m.fileCursor >= len(m.visible) {
		m.fileCursor = max(0, len(m.visible)-1)
	}
	m.ensureFileVisible()
}

func (m *Model) ensureFileVisible() {
	_, _, innerH, _, _ := m.layout()
	m.ensureFileVisibleHeight(max(1, innerH-4))
}

func (m *Model) ensureFileVisibleHeight(height int) {
	if m.fileCursor < m.fileOffset {
		m.fileOffset = m.fileCursor
	}
	if m.fileCursor >= m.fileOffset+height {
		m.fileOffset = m.fileCursor - height + 1
	}
	maxOffset := max(0, len(m.visible)-height)
	m.fileOffset = min(max(0, m.fileOffset), maxOffset)
}

func (m *Model) expandSelected() tea.Cmd {
	if len(m.visible) == 0 {
		return nil
	}
	node := m.visible[m.fileCursor]
	if !node.Dir {
		return nil
	}
	if node.Expanded {
		node.Expanded = false
		node.Loaded, node.Loading = false, true
		m.refreshFiles()
		return loadDir(m.conn.SFTP, node)
	}
	node.Expanded, node.Loaded, node.Loading = true, false, true
	m.refreshFiles()
	return loadDir(m.conn.SFTP, node)
}

func (m *Model) collapseSelected() tea.Cmd {
	if len(m.visible) == 0 {
		return nil
	}
	node := m.visible[m.fileCursor]
	if node.Dir && node.Expanded {
		node.Expanded, node.Loaded, node.Loading = false, false, true
		m.refreshFiles()
		return loadDir(m.conn.SFTP, node)
	}
	for i := m.fileCursor - 1; i >= 0; i-- {
		if m.visible[i].Depth < node.Depth {
			m.fileCursor = i
			return nil
		}
	}
	if node == m.root && path.Clean(node.Path) != "/" {
		return m.openRemoteDirectory(path.Dir(node.Path))
	}
	return nil
}

func (m *Model) openRemoteDirectory(directory string) tea.Cmd {
	if m.conn == nil {
		return nil
	}
	directory = path.Clean(directory)
	if !path.IsAbs(directory) {
		return nil
	}
	m.root = &fileNode{Name: directory, Path: directory, Dir: true, Expanded: true, Loading: true}
	m.visible = []*fileNode{m.root}
	m.fileCursor, m.fileOffset = 0, 0
	m.status = "SFTP: загрузка " + directory
	return loadDir(m.conn.SFTP, m.root)
}

func (m *Model) reloadSelectedDirectory() tea.Cmd {
	if len(m.visible) == 0 || m.conn == nil {
		return nil
	}
	node := m.visible[m.fileCursor]
	if !node.Dir {
		node = findParentFileNode(m.root, node)
	}
	if node == nil {
		return nil
	}
	node.Loaded, node.Loading = false, true
	m.status = "SFTP: обновление " + node.Path
	return loadDir(m.conn.SFTP, node)
}

func (m *Model) cancelTransfer() {
	if !m.transferring || m.transferCancel == nil {
		return
	}
	m.status = "Отмена передачи файлов…"
	m.transferCancel()
}

func (m *Model) startSelectedDownload(destination string, node *fileNode) tea.Cmd {
	if m.transferring || node == nil || m.conn == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	updates := make(chan transferProgressMsg, 16)
	m.transferring, m.transferCh, m.transferCancel = true, updates, cancel
	m.transferDone, m.transferTotal, m.transferPath, m.transferErr = 0, 0, node.Path, nil
	m.transferUpload = false
	m.status = "Подсчёт размера и скачивание " + node.Path
	go startDownload(ctx, m.conn.SFTP, node, destination, updates)
	return waitTransfer(updates)
}

func (m *Model) openDownloadPicker() tea.Cmd {
	if m.transferring {
		m.status = "Дождитесь завершения текущей передачи"
		return nil
	}
	if len(m.visible) == 0 || m.conn == nil {
		return nil
	}
	picker, cmd := newLocalPicker(pickerDownloadDestination, m.visible[m.fileCursor])
	m.picker = picker
	return cmd
}

func (m *Model) openUploadPicker() tea.Cmd {
	if m.transferring {
		m.status = "Дождитесь завершения текущей передачи"
		return nil
	}
	if len(m.visible) == 0 || m.conn == nil {
		return nil
	}
	target := m.visible[m.fileCursor]
	if !target.Dir {
		m.status = "Для загрузки выберите удалённый каталог"
		return nil
	}
	picker, cmd := newLocalPicker(pickerUploadSource, target)
	m.picker = picker
	return cmd
}

func (m *Model) chooseUploadSources() tea.Cmd {
	if m.picker == nil || m.picker.loading {
		return nil
	}
	var sources []string
	for source, selected := range m.picker.marked {
		if selected {
			sources = append(sources, source)
		}
	}
	if len(sources) == 0 {
		selected := m.picker.selected()
		if selected == nil {
			m.picker.err = fmt.Errorf("выберите локальный файл или каталог")
			return nil
		}
		sources = append(sources, selected.path)
	}
	sort.Strings(sources)
	target := m.picker.remoteTarget
	m.picker = nil
	return m.startUpload(sources, target)
}

func (m *Model) startUpload(localSources []string, target *fileNode) tea.Cmd {
	if m.transferring || target == nil || !target.Dir || m.conn == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	updates := make(chan transferProgressMsg, 16)
	m.transferring, m.transferCh, m.transferCancel = true, updates, cancel
	m.transferDone, m.transferTotal, m.transferPath, m.transferErr = 0, 0, strings.Join(localSources, ", "), nil
	m.transferUpload, m.transferRemoteTarget = true, target
	m.status = fmt.Sprintf("Подсчёт размера и загрузка %d объект(ов) в %s", len(localSources), target.Path)
	go startUploads(ctx, m.conn.SFTP, localSources, target.Path, updates)
	return waitTransfer(updates)
}

func (m *Model) captureTab() *tabState {
	return &tabState{
		id: m.tabID, page: m.page, status: m.status, selected: m.selected, confirm: m.confirm,
		groupInput: m.groupInput, groupCursor: m.groupCursor,
		inputs: m.inputs, formFocus: m.formFocus, testing: m.testing, connecting: m.connecting,
		profile: m.profile, conn: m.conn, term: m.term,
		filesFocused: m.filesFocused, sftpHidden: m.sftpHidden, root: m.root, visible: m.visible,
		fileCursor: m.fileCursor, fileOffset: m.fileOffset, picker: m.picker,
		escapePending: m.escapePending, escapeID: m.escapeID,
		transferring: m.transferring, transferCh: m.transferCh, transferDone: m.transferDone,
		transferTotal: m.transferTotal, transferPath: m.transferPath, transferErr: m.transferErr,
		transferUpload: m.transferUpload, transferCancel: m.transferCancel,
		transferRemoteTarget: m.transferRemoteTarget, forwardModal: m.forwardModal, forwards: m.forwards,
		hostKeyPrompt: m.hostKeyPrompt,
	}
}

func (m *Model) saveActiveTab() {
	if m.activeTab >= 0 && m.activeTab < len(m.tabs) {
		m.tabs[m.activeTab] = m.captureTab()
	}
}

func (m *Model) restoreTab(index int) {
	state := m.tabs[index]
	m.activeTab, m.tabID, m.page, m.status = index, state.id, state.page, state.status
	m.selected, m.confirm = state.selected, state.confirm
	m.groupInput, m.groupCursor = state.groupInput, state.groupCursor
	m.inputs, m.formFocus, m.testing, m.connecting = state.inputs, state.formFocus, state.testing, state.connecting
	m.profile, m.conn, m.term = state.profile, state.conn, state.term
	m.filesFocused, m.sftpHidden = state.filesFocused, state.sftpHidden
	m.root, m.visible, m.fileCursor, m.fileOffset = state.root, state.visible, state.fileCursor, state.fileOffset
	m.picker, m.escapePending, m.escapeID = state.picker, state.escapePending, state.escapeID
	m.transferring, m.transferCh = state.transferring, state.transferCh
	m.transferDone, m.transferTotal, m.transferPath = state.transferDone, state.transferTotal, state.transferPath
	m.transferErr, m.transferUpload, m.transferCancel = state.transferErr, state.transferUpload, state.transferCancel
	m.transferRemoteTarget = state.transferRemoteTarget
	m.forwardModal, m.forwards = state.forwardModal, state.forwards
	m.hostKeyPrompt = state.hostKeyPrompt
}

func (m *Model) resetTab(id uint64) {
	m.tabID, m.page, m.status, m.selected, m.confirm = id, pageSessions, "", 0, false
	m.groupInput = nil
	m.groupCursor = -1
	if order := m.sessionOrder(); len(order) > 0 {
		m.selected = order[0]
	}
	m.testing, m.connecting = false, false
	m.profile, m.conn, m.term = config.Session{}, nil, nil
	m.filesFocused, m.sftpHidden = false, true
	m.root, m.visible, m.fileCursor, m.fileOffset = nil, nil, 0, 0
	m.picker, m.escapePending, m.escapeID = nil, false, 0
	m.transferring, m.transferCh, m.transferCancel = false, nil, nil
	m.transferDone, m.transferTotal, m.transferPath, m.transferErr = 0, 0, "", nil
	m.transferUpload, m.transferRemoteTarget = false, nil
	m.forwardModal, m.forwards = nil, nil
	m.hostKeyPrompt = nil
	m.resetForm()
}

func (m *Model) openTab() {
	m.saveActiveTab()
	m.nextTabID++
	m.resetTab(m.nextTabID)
	m.activeTab = len(m.tabs)
	m.tabs = append(m.tabs, m.captureTab())
}

func (m *Model) switchTab(index int) {
	if index < 0 || index >= len(m.tabs) || index == m.activeTab {
		return
	}
	m.saveActiveTab()
	m.restoreTab(index)
	if m.page == pageConnected {
		m.resizeRemote()
	}
}

func closeTabResources(state *tabState) {
	if state == nil {
		return
	}
	if state.transferCancel != nil {
		state.transferCancel()
	}
	if state.conn != nil {
		state.conn.Close()
	}
}

func (m *Model) closeActiveTab() {
	m.saveActiveTab()
	closeTabResources(m.tabs[m.activeTab])
	if len(m.tabs) == 1 {
		m.nextTabID++
		m.resetTab(m.nextTabID)
		m.tabs[0] = m.captureTab()
		m.activeTab = 0
		return
	}
	closed := m.activeTab
	m.tabs = append(m.tabs[:closed], m.tabs[closed+1:]...)
	m.restoreTab(min(closed, len(m.tabs)-1))
	if m.page == pageConnected {
		m.resizeRemote()
	}
}

func (m *Model) tabIndexByID(id uint64) int {
	if m.tabID == id {
		return m.activeTab
	}
	for index, state := range m.tabs {
		if index != m.activeTab && state.id == id {
			return index
		}
	}
	return -1
}

func (m *Model) messageTabIndex(message tea.Msg) (int, bool) {
	switch msg := message.(type) {
	case connectMsg:
		return m.tabIndexByID(msg.tabID), true
	case keyTestMsg:
		return m.tabIndexByID(msg.tabID), true
	case escapeTimeoutMsg:
		return m.tabIndexByID(msg.tabID), true
	case shellOutputMsg:
		if m.conn == msg.source {
			return m.activeTab, true
		}
		for index, state := range m.tabs {
			if index != m.activeTab && state.conn == msg.source {
				return index, true
			}
		}
		return -1, true
	case dirLoadedMsg:
		if treeContains(m.root, msg.node) {
			return m.activeTab, true
		}
		for index, state := range m.tabs {
			if index != m.activeTab && treeContains(state.root, msg.node) {
				return index, true
			}
		}
		return -1, true
	case localDirLoadedMsg:
		if m.picker == msg.picker {
			return m.activeTab, true
		}
		for index, state := range m.tabs {
			if index != m.activeTab && state.picker == msg.picker {
				return index, true
			}
		}
		return -1, true
	case transferProgressMsg:
		if m.transferCh == msg.source {
			return m.activeTab, true
		}
		for index, state := range m.tabs {
			if index != m.activeTab && state.transferCh == msg.source {
				return index, true
			}
		}
		return -1, true
	case forwardStartedMsg:
		return m.tabIndexByID(msg.tabID), true
	case forwardErrorMsg:
		return m.tabIndexByID(msg.tabID), true
	default:
		return -1, false
	}
}

func (m *Model) updateInactiveTab(index int, message tea.Msg) (tea.Model, tea.Cmd) {
	current := m.activeTab
	m.saveActiveTab()
	m.restoreTab(index)
	_, cmd := m.Update(message)
	m.saveActiveTab()
	m.restoreTab(current)
	return m, cmd
}

func (m *Model) disconnect(status string) {
	if m.transferCancel != nil {
		m.transferCancel()
	}
	if m.conn != nil {
		m.conn.Close()
	}
	m.conn, m.root, m.visible, m.term = nil, nil, nil, nil
	m.transferring, m.transferCh, m.transferCancel = false, nil, nil
	m.picker, m.transferRemoteTarget, m.escapePending = nil, nil, false
	m.forwardModal, m.forwards = nil, nil
	m.hostKeyPrompt = nil
	m.page, m.status = pageSessions, status
}

func (m *Model) close() {
	m.saveActiveTab()
	for _, state := range m.tabs {
		closeTabResources(state)
	}
}

func connectCommand(tabID uint64, profile config.Session, width, height int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		conn, err := remote.Connect(ctx, profile, width, height, false)
		return connectMsg{conn: conn, profile: profile, err: err, tabID: tabID}
	}
}

func testKeyCommand(tabID uint64, profile config.Session) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return keyTestMsg{err: remote.TestKey(ctx, profile), tabID: tabID, profile: profile}
	}
}

func waitShellOutput(conn *remote.Connection) tea.Cmd {
	return func() tea.Msg {
		data, ok := <-conn.Output
		return shellOutputMsg{data: data, ok: ok, source: conn}
	}
}

func waitTransfer(updates <-chan transferProgressMsg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-updates
		if !ok {
			return transferProgressMsg{err: fmt.Errorf("поток передачи неожиданно закрыт"), final: true, source: updates}
		}
		msg.source = updates
		return msg
	}
}

func escapeTimeoutCommand(tabID, id uint64) tea.Cmd {
	return tea.Tick(700*time.Millisecond, func(time.Time) tea.Msg {
		return escapeTimeoutMsg{id: id, tabID: tabID}
	})
}

func treeContains(root, wanted *fileNode) bool {
	if root == nil {
		return false
	}
	if root == wanted {
		return true
	}
	for _, child := range root.Children {
		if treeContains(child, wanted) {
			return true
		}
	}
	return false
}

func findFileNode(root *fileNode, remotePath string) *fileNode {
	if root == nil {
		return nil
	}
	if path.Clean(root.Path) == path.Clean(remotePath) && root.Dir {
		return root
	}
	for _, child := range root.Children {
		if found := findFileNode(child, remotePath); found != nil {
			return found
		}
	}
	return nil
}

func findParentFileNode(root, child *fileNode) *fileNode {
	if root == nil || child == nil {
		return nil
	}
	for _, candidate := range root.Children {
		if candidate == child {
			return root
		}
		if parent := findParentFileNode(candidate, child); parent != nil {
			return parent
		}
	}
	return nil
}

func keyBytes(key tea.KeyPressMsg) []byte {
	switch key.String() {
	case "enter":
		return []byte{'\r'}
	case "tab":
		return []byte{'\t'}
	case "shift+tab":
		return []byte("\x1b[Z")
	case "backspace":
		return []byte{0x7f}
	case "esc":
		return []byte{0x1b}
	case "up":
		return []byte("\x1b[A")
	case "down":
		return []byte("\x1b[B")
	case "right":
		return []byte("\x1b[C")
	case "left":
		return []byte("\x1b[D")
	case "home":
		return []byte("\x1b[H")
	case "end":
		return []byte("\x1b[F")
	case "delete":
		return []byte("\x1b[3~")
	case "pgup":
		return []byte("\x1b[5~")
	case "pgdown":
		return []byte("\x1b[6~")
	case "insert":
		return []byte("\x1b[2~")
	case "f1":
		return []byte("\x1bOP")
	case "f2":
		return []byte("\x1bOQ")
	case "f3":
		return []byte("\x1bOR")
	case "f4":
		return []byte("\x1bOS")
	case "f5":
		return []byte("\x1b[15~")
	case "f6":
		return []byte("\x1b[17~")
	case "f7":
		return []byte("\x1b[18~")
	case "f8":
		return []byte("\x1b[19~")
	case "f9":
		return []byte("\x1b[20~")
	case "f10":
		return []byte("\x1b[21~")
	case "f11":
		return []byte("\x1b[23~")
	case "f12":
		return []byte("\x1b[24~")
	case "ctrl+up":
		return []byte("\x1b[1;5A")
	case "ctrl+down":
		return []byte("\x1b[1;5B")
	case "ctrl+right":
		return []byte("\x1b[1;5C")
	case "ctrl+left":
		return []byte("\x1b[1;5D")
	case "shift+up":
		return []byte("\x1b[1;2A")
	case "shift+down":
		return []byte("\x1b[1;2B")
	case "shift+right":
		return []byte("\x1b[1;2C")
	case "shift+left":
		return []byte("\x1b[1;2D")
	}
	if key.Text != "" {
		data := []byte(key.Text)
		if key.Mod.Contains(tea.ModAlt) {
			return append([]byte{0x1b}, data...)
		}
		return data
	}
	s := key.Keystroke()
	if key.Mod.Contains(tea.ModCtrl) && len(s) == len("ctrl+x") {
		c := byte(key.Code)
		if c >= 'a' && c <= 'z' {
			return []byte{c - 'a' + 1}
		}
	}
	return nil
}

func currentUser() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return ""
}

func statusLine(status string) string {
	status = safeDisplayText(status)
	color := muted
	if strings.HasPrefix(status, "Ошибка") || strings.HasPrefix(status, "Не удалось") || strings.Contains(status, "не прошёл") {
		color = errorColor
	} else if strings.HasPrefix(status, "✓") || strings.HasPrefix(status, "Подключено") || strings.HasPrefix(status, "Скачано") || strings.HasPrefix(status, "Загружено") {
		color = successColor
	}
	return lipgloss.NewStyle().Foreground(color).Render("● " + status)
}

func renderHints(hints ...[2]string) string {
	parts := make([]string, 0, len(hints))
	for _, hint := range hints {
		parts = append(parts, keyStyle.Render(hint[0])+" "+lipgloss.NewStyle().Foreground(muted).Render(hint[1]))
	}
	return strings.Join(parts, "  ")
}

func warningColorStyle(value string) string {
	return lipgloss.NewStyle().Foreground(warningColor).Render(value)
}

func pickerEntryLine(entry localEntry, icon string, width int) string {
	if width <= 0 {
		return ""
	}
	badge := ""
	if !entry.dir {
		badge = sizeStyle.Render("📦 " + formatBytes(entry.size))
	}
	separator := "  "
	reserved := lipgloss.Width(icon) + lipgloss.Width(separator) + lipgloss.Width(badge)
	if badge == "" {
		separator = ""
		reserved = lipgloss.Width(icon)
	} else if width-reserved < 3 {
		badge, separator = "", ""
		reserved = lipgloss.Width(icon)
	}
	name := ellipsize(entry.name, max(1, width-reserved))
	return icon + name + separator + badge
}

func ellipsize(value string, width int) string {
	value = safeDisplayText(value)
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	if width == 1 {
		return "…"
	}
	return lipgloss.NewStyle().MaxWidth(width-1).Render(value) + "…"
}

func truncate(value string, width int) string {
	if width <= 0 {
		return ""
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(safeDisplayText(value))
}

func safeDisplayText(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '�'
		}
		return r
	}, value)
}
