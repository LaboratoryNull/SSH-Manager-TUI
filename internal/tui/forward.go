package tui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"ssh-manager-tui/internal/config"
	"ssh-manager-tui/internal/remote"
)

type forwardRuntime struct {
	forward    *remote.Forward
	starting   bool
	err        string
	address    string
	generation uint64
}

type forwardModal struct {
	cursor int
	editor *forwardEditor
	status string
}

type forwardEditor struct {
	inputs    []textinput.Model
	focus     int
	editIndex int
	typeValue config.ForwardType
	autoStart bool
}

type forwardStartedMsg struct {
	tabID      uint64
	generation uint64
	spec       config.PortForward
	forward    *remote.Forward
	err        error
}

type forwardErrorMsg struct {
	tabID   uint64
	name    string
	forward *remote.Forward
	err     error
	ok      bool
}

func startForwardCommand(tabID, generation uint64, connection *remote.Connection, spec config.PortForward) tea.Cmd {
	return func() tea.Msg {
		forward, err := connection.StartForward(spec)
		return forwardStartedMsg{tabID: tabID, generation: generation, spec: spec, forward: forward, err: err}
	}
}

func waitForwardError(tabID uint64, name string, forward *remote.Forward) tea.Cmd {
	return func() tea.Msg {
		err, ok := <-forward.Errors()
		return forwardErrorMsg{tabID: tabID, name: name, forward: forward, err: err, ok: ok}
	}
}

func forwardKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func (m *Model) startForward(spec config.PortForward) tea.Cmd {
	if m.conn == nil {
		return nil
	}
	if m.forwards == nil {
		m.forwards = make(map[string]*forwardRuntime)
	}
	key := forwardKey(spec.Name)
	runtime := m.forwards[key]
	if runtime == nil {
		runtime = &forwardRuntime{}
		m.forwards[key] = runtime
	}
	if runtime.starting || runtime.forward != nil {
		return nil
	}
	m.nextForwardID++
	runtime.starting, runtime.err, runtime.address, runtime.generation = true, "", "", m.nextForwardID
	return startForwardCommand(m.tabID, runtime.generation, m.conn, spec)
}

func (m *Model) stopForward(name string) {
	key := forwardKey(name)
	runtime := m.forwards[key]
	if runtime == nil {
		return
	}
	if runtime.forward != nil {
		runtime.forward.Close()
	}
	runtime.forward, runtime.starting, runtime.err, runtime.address = nil, false, "", ""
}

func (m *Model) handleForwardStarted(msg forwardStartedMsg) (tea.Model, tea.Cmd) {
	key := forwardKey(msg.spec.Name)
	runtime := m.forwards[key]
	if runtime == nil || !runtime.starting || runtime.generation != msg.generation || m.conn == nil {
		if msg.forward != nil {
			msg.forward.Close()
		}
		return m, nil
	}
	runtime.starting = false
	if msg.err != nil {
		runtime.err = msg.err.Error()
		if m.forwardModal != nil {
			m.forwardModal.status = "Ошибка запуска «" + msg.spec.Name + "»: " + msg.err.Error()
		}
		return m, nil
	}
	runtime.forward, runtime.address, runtime.err = msg.forward, msg.forward.Address(), ""
	if m.forwardModal != nil {
		m.forwardModal.status = "Туннель «" + msg.spec.Name + "» запущен"
	}
	return m, waitForwardError(m.tabID, msg.spec.Name, msg.forward)
}

func (m *Model) handleForwardError(msg forwardErrorMsg) (tea.Model, tea.Cmd) {
	runtime := m.forwards[forwardKey(msg.name)]
	if runtime == nil || runtime.forward != msg.forward {
		return m, nil
	}
	if !msg.ok {
		runtime.forward, runtime.address = nil, ""
		return m, nil
	}
	runtime.err = msg.err.Error()
	runtime.forward, runtime.address = nil, ""
	msg.forward.Close()
	if m.forwardModal != nil {
		m.forwardModal.status = "Туннель «" + msg.name + "» остановлен: " + msg.err.Error()
	}
	return m, nil
}

func newForwardEditor(spec *config.PortForward, editIndex int) *forwardEditor {
	value := config.PortForward{
		Type: config.ForwardLocal, BindHost: "127.0.0.1", BindPort: 8080,
		TargetHost: "127.0.0.1", TargetPort: 80,
	}
	if spec != nil {
		value = *spec
	}
	values := []struct {
		placeholder string
		value       string
	}{
		{"например, postgres", value.Name},
		{"127.0.0.1", value.BindHost},
		{"8080", strconv.Itoa(value.BindPort)},
		{"database.internal", value.TargetHost},
		{"5432", portValue(value.TargetPort)},
	}
	editor := &forwardEditor{editIndex: editIndex, typeValue: value.Type, autoStart: value.AutoStart}
	editor.inputs = make([]textinput.Model, len(values))
	for index, item := range values {
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
		input.SetWidth(36)
		input.CharLimit = 255
		editor.inputs[index] = input
	}
	editor.setFocus(0)
	return editor
}

func portValue(port int) string {
	if port == 0 {
		return ""
	}
	return strconv.Itoa(port)
}

func (e *forwardEditor) inputIndex() int {
	switch e.focus {
	case 0:
		return 0
	case 2:
		return 1
	case 3:
		return 2
	case 4:
		return 3
	case 5:
		return 4
	default:
		return -1
	}
}

func (e *forwardEditor) setFocus(focus int) {
	for index := range e.inputs {
		e.inputs[index].Blur()
	}
	e.focus = (focus + 9) % 9
	if inputIndex := e.inputIndex(); inputIndex >= 0 {
		e.inputs[inputIndex].Focus()
	}
}

func (e *forwardEditor) moveFocus(direction int) {
	next := (e.focus + direction + 9) % 9
	if e.typeValue == config.ForwardDynamic {
		for next == 4 || next == 5 {
			next = (next + direction + 9) % 9
		}
	}
	e.setFocus(next)
}

func (e *forwardEditor) cycleType(direction int) {
	types := []config.ForwardType{config.ForwardLocal, config.ForwardRemote, config.ForwardDynamic}
	index := 0
	for i, forwardType := range types {
		if forwardType == e.typeValue {
			index = i
			break
		}
	}
	e.typeValue = types[(index+direction+len(types))%len(types)]
}

func (e *forwardEditor) value() (config.PortForward, error) {
	bindPort, err := strconv.Atoi(strings.TrimSpace(e.inputs[2].Value()))
	if err != nil {
		return config.PortForward{}, fmt.Errorf("локальный порт должен быть числом")
	}
	targetPort := 0
	if e.typeValue != config.ForwardDynamic {
		targetPort, err = strconv.Atoi(strings.TrimSpace(e.inputs[4].Value()))
		if err != nil {
			return config.PortForward{}, fmt.Errorf("целевой порт должен быть числом")
		}
	}
	forward := config.PortForward{
		Name: strings.TrimSpace(e.inputs[0].Value()), Type: e.typeValue,
		BindHost: strings.TrimSpace(e.inputs[1].Value()), BindPort: bindPort,
		TargetHost: strings.TrimSpace(e.inputs[3].Value()), TargetPort: targetPort, AutoStart: e.autoStart,
	}
	if e.typeValue == config.ForwardDynamic {
		forward.TargetHost, forward.TargetPort = "", 0
	}
	if err := forward.Validate(); err != nil {
		return config.PortForward{}, err
	}
	return forward, nil
}

func (m *Model) handleForwardKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	modal := m.forwardModal
	if modal.editor != nil {
		return m.handleForwardEditorKey(key)
	}
	switch key.Keystroke() {
	case "esc":
		m.forwardModal = nil
	case "up", "k":
		if modal.cursor > 0 {
			modal.cursor--
		}
	case "down", "j":
		if modal.cursor+1 < len(m.profile.PortForwards) {
			modal.cursor++
		}
	case "n":
		modal.editor = newForwardEditor(nil, -1)
		modal.status = ""
	case "e":
		if len(m.profile.PortForwards) > 0 {
			spec := m.profile.PortForwards[modal.cursor]
			modal.editor = newForwardEditor(&spec, modal.cursor)
			modal.status = ""
		}
	case "d":
		return m, m.deleteSelectedForward()
	case "enter", "space":
		if len(m.profile.PortForwards) == 0 {
			return m, nil
		}
		spec := m.profile.PortForwards[modal.cursor]
		runtime := m.forwards[forwardKey(spec.Name)]
		if runtime != nil && (runtime.forward != nil || runtime.starting) {
			m.stopForward(spec.Name)
			modal.status = "Туннель «" + spec.Name + "» остановлен"
			return m, nil
		}
		modal.status = "Запуск туннеля «" + spec.Name + "»…"
		return m, m.startForward(spec)
	}
	return m, nil
}

func (m *Model) handleForwardEditorKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	modal, editor := m.forwardModal, m.forwardModal.editor
	switch key.Keystroke() {
	case "esc":
		modal.editor = nil
		modal.status = "Изменения отменены"
		return m, nil
	case "tab", "down":
		editor.moveFocus(1)
		return m, nil
	case "shift+tab", "up":
		editor.moveFocus(-1)
		return m, nil
	case "left":
		if editor.focus == 1 {
			editor.cycleType(-1)
			return m, nil
		}
		if editor.focus == 6 {
			editor.autoStart = !editor.autoStart
			return m, nil
		}
	case "right", "space":
		if editor.focus == 1 {
			editor.cycleType(1)
			return m, nil
		}
		if editor.focus == 6 {
			editor.autoStart = !editor.autoStart
			return m, nil
		}
	case "enter":
		switch editor.focus {
		case 1:
			editor.cycleType(1)
		case 6:
			editor.autoStart = !editor.autoStart
		case 7:
			return m, m.saveForwardEditor()
		case 8:
			modal.editor = nil
			modal.status = "Изменения отменены"
		default:
			editor.moveFocus(1)
		}
		return m, nil
	}
	if inputIndex := editor.inputIndex(); inputIndex >= 0 {
		var command tea.Cmd
		editor.inputs[inputIndex], command = editor.inputs[inputIndex].Update(key)
		return m, command
	}
	return m, nil
}

func (m *Model) saveForwardEditor() tea.Cmd {
	modal, editor := m.forwardModal, m.forwardModal.editor
	spec, err := editor.value()
	if err != nil {
		modal.status = "Ошибка: " + err.Error()
		return nil
	}
	for index, existing := range m.profile.PortForwards {
		if index != editor.editIndex && forwardKey(existing.Name) == forwardKey(spec.Name) {
			modal.status = "Ошибка: имя туннеля уже используется"
			return nil
		}
	}
	forwards := append([]config.PortForward(nil), m.profile.PortForwards...)
	oldName := ""
	wasRunning := false
	if editor.editIndex >= 0 {
		oldName = forwards[editor.editIndex].Name
		runtime := m.forwards[forwardKey(oldName)]
		wasRunning = runtime != nil && (runtime.forward != nil || runtime.starting)
		forwards[editor.editIndex] = spec
	} else {
		forwards = append(forwards, spec)
	}
	if err := m.persistPortForwards(forwards); err != nil {
		modal.status = "Ошибка сохранения: " + err.Error()
		return nil
	}
	if oldName != "" {
		m.stopForward(oldName)
		delete(m.forwards, forwardKey(oldName))
	}
	modal.editor = nil
	modal.cursor = len(forwards) - 1
	if editor.editIndex >= 0 {
		modal.cursor = editor.editIndex
	}
	modal.status = "Туннель «" + spec.Name + "» сохранён"
	if isPublicBind(spec.BindHost) {
		modal.status += " • внимание: порт доступен не только локально"
	}
	if wasRunning || (editor.editIndex < 0 && spec.AutoStart) {
		return m.startForward(spec)
	}
	return nil
}

func (m *Model) deleteSelectedForward() tea.Cmd {
	modal := m.forwardModal
	if len(m.profile.PortForwards) == 0 {
		return nil
	}
	spec := m.profile.PortForwards[modal.cursor]
	forwards := append([]config.PortForward(nil), m.profile.PortForwards[:modal.cursor]...)
	forwards = append(forwards, m.profile.PortForwards[modal.cursor+1:]...)
	if err := m.persistPortForwards(forwards); err != nil {
		modal.status = "Ошибка удаления: " + err.Error()
		return nil
	}
	m.stopForward(spec.Name)
	delete(m.forwards, forwardKey(spec.Name))
	modal.cursor = min(modal.cursor, max(0, len(forwards)-1))
	modal.status = "Туннель «" + spec.Name + "» удалён"
	return nil
}

func (m *Model) persistPortForwards(forwards []config.PortForward) error {
	index := -1
	for i := range m.config.Sessions {
		if m.config.Sessions[i].Name == m.profile.Name {
			index = i
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("хост %q не найден", m.profile.Name)
	}
	old := m.config.Sessions[index]
	stored := old
	stored.PortForwards = append([]config.PortForward(nil), forwards...)
	m.config.Sessions[index] = stored
	if err := m.store.Save(m.config); err != nil {
		m.config.Sessions[index] = old
		return err
	}
	m.profile.PortForwards = append([]config.PortForward(nil), forwards...)
	return nil
}

func isPublicBind(host string) bool {
	host = strings.TrimSpace(host)
	return host == "0.0.0.0" || host == "::" || host == "[::]"
}

func (m *Model) forwardView() string {
	modal := m.forwardModal
	if modal == nil {
		return m.connectedView()
	}
	boxWidth := min(96, max(42, m.width-6))
	boxHeight := min(30, max(16, m.contentHeight()-4))
	innerWidth := max(12, boxWidth-6)
	var lines []string
	if modal.editor != nil {
		lines = m.forwardEditorLines(modal.editor, innerWidth)
	} else {
		lines = m.forwardListLines(innerWidth, boxHeight)
	}
	for len(lines) < boxHeight-5 {
		lines = append(lines, "")
	}
	if modal.status != "" {
		lines = append(lines, statusLine(truncate(modal.status, innerWidth)))
	} else {
		lines = append(lines, "")
	}
	var help string
	if modal.editor != nil {
		help = hintsView(innerWidth,
			[][2]string{{"Tab/↑/↓", "переход"}, {"←/→", "значение"}, {"Enter", "выбрать"}, {"Esc", "назад"}},
			[][2]string{{"Tab", "переход"}, {"Enter", "выбрать"}, {"Esc", "назад"}},
		)
	} else {
		help = hintsView(innerWidth,
			[][2]string{{"Enter", "запуск/стоп"}, {"n", "новый"}, {"e", "изменить"}, {"d", "удалить"}, {"Esc", "закрыть"}},
			[][2]string{{"Enter", "старт"}, {"n/e/d", "управление"}, {"Esc", "закрыть"}},
		)
	}
	lines = append(lines, help)
	box := borderStyle.Copy().BorderForeground(accentWarm).Padding(1, 2).
		Width(boxWidth - 2).Height(boxHeight - 2).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, m.contentHeight(), lipgloss.Center, lipgloss.Center, box)
}

func (m *Model) forwardListLines(width, boxHeight int) []string {
	lines := []string{
		titleStyle.Render("◆ Проброс портов"),
		subtitleStyle.Render(currentPlatformKeys().ports+" • ") + lipgloss.NewStyle().Foreground(muted).Render(m.profile.Name),
		"",
	}
	if len(m.profile.PortForwards) == 0 {
		return append(lines,
			lipgloss.NewStyle().Foreground(muted).Italic(true).Render("Туннелей пока нет."),
			lipgloss.NewStyle().Foreground(muted).Render("Нажмите n, чтобы создать Local, Remote или SOCKS5-туннель."),
		)
	}
	listHeight := max(1, boxHeight-10)
	start := 0
	if m.forwardModal.cursor >= listHeight {
		start = m.forwardModal.cursor - listHeight + 1
	}
	end := min(len(m.profile.PortForwards), start+listHeight)
	for index := start; index < end; index++ {
		spec := m.profile.PortForwards[index]
		runtime := m.forwards[forwardKey(spec.Name)]
		indicator, color := "○", muted
		if runtime != nil {
			switch {
			case runtime.starting:
				indicator, color = "◌", warningColor
			case runtime.forward != nil:
				indicator, color = "●", successColor
			case runtime.err != "":
				indicator, color = "×", errorColor
			}
		}
		typeBadge := lipgloss.NewStyle().Foreground(accentAlt).Render(forwardTypeShort(spec.Type))
		auto := ""
		if spec.AutoStart {
			auto = warningColorStyle(" ⚡")
		}
		line := lipgloss.NewStyle().Foreground(color).Render(indicator) + " " + typeBadge + " " +
			safeDisplayText(spec.Name) + auto + "  " + lipgloss.NewStyle().Foreground(muted).Render(forwardRoute(spec))
		line = lipgloss.NewStyle().MaxWidth(width).Render(line)
		style := lipgloss.NewStyle().Width(width)
		if index == m.forwardModal.cursor {
			style = selectedStyle.Copy().Width(width)
		}
		lines = append(lines, style.Render(line))
		if runtime != nil && runtime.err != "" && index == m.forwardModal.cursor {
			lines = append(lines, lipgloss.NewStyle().Foreground(errorColor).Render(truncate("  ↳ "+runtime.err, width)))
		}
	}
	return lines
}

func (m *Model) forwardEditorLines(editor *forwardEditor, width int) []string {
	title := "Новый туннель"
	if editor.editIndex >= 0 {
		title = "Изменить туннель"
	}
	lines := []string{titleStyle.Render("◆ " + title), ""}
	labels := []string{"Название", "Тип", "Bind-адрес", "Bind-порт", "Целевой хост", "Целевой порт", "Автозапуск"}
	for logical := 0; logical <= 6; logical++ {
		labelStyle := lipgloss.NewStyle().Width(18).Foreground(muted)
		if editor.focus == logical {
			labelStyle = labelStyle.Foreground(accentAlt).Bold(true)
		}
		line := labelStyle.Render(labels[logical])
		switch logical {
		case 1:
			line += forwardTypeControl(editor.typeValue, editor.focus == logical)
		case 6:
			value := "○ выключен"
			if editor.autoStart {
				value = "⚡ включён"
			}
			line += controlStyle(value, editor.focus == logical)
		default:
			inputIndex := map[int]int{0: 0, 2: 1, 3: 2, 4: 3, 5: 4}[logical]
			if editor.typeValue == config.ForwardDynamic && (logical == 4 || logical == 5) {
				line += lipgloss.NewStyle().Foreground(borderMuted).Italic(true).Render("не используется для SOCKS5")
			} else {
				line += editor.inputs[inputIndex].View()
			}
		}
		lines = append(lines, line)
	}
	lines = append(lines, "")
	buttons := []string{"Сохранить", "Отмена"}
	var rendered []string
	for index, label := range buttons {
		rendered = append(rendered, controlStyle(label, editor.focus == 7+index))
	}
	lines = append(lines, strings.Join(rendered, "  "))
	if isPublicBind(editor.inputs[1].Value()) {
		lines = append(lines, lipgloss.NewStyle().Foreground(warningColor).Render(truncate("⚠ Этот bind-адрес может открыть порт внешней сети", width)))
	}
	return lines
}

func controlStyle(value string, focused bool) string {
	style := lipgloss.NewStyle().Padding(0, 1).Foreground(muted).Background(surface)
	if focused {
		style = style.Foreground(lipgloss.Color("#11111B")).Background(accent).Bold(true)
	}
	return style.Render(value)
}

func forwardTypeControl(forwardType config.ForwardType, focused bool) string {
	return controlStyle("← "+forwardTypeLabel(forwardType)+" →", focused)
}

func forwardTypeLabel(forwardType config.ForwardType) string {
	switch forwardType {
	case config.ForwardRemote:
		return "Remote (-R)"
	case config.ForwardDynamic:
		return "SOCKS5 (-D)"
	default:
		return "Local (-L)"
	}
}

func forwardTypeShort(forwardType config.ForwardType) string {
	switch forwardType {
	case config.ForwardRemote:
		return "R"
	case config.ForwardDynamic:
		return "D"
	default:
		return "L"
	}
}

func forwardRoute(spec config.PortForward) string {
	bind := spec.BindHost + ":" + strconv.Itoa(spec.BindPort)
	if spec.Type == config.ForwardDynamic {
		return bind + " → SOCKS5"
	}
	target := spec.TargetHost + ":" + strconv.Itoa(spec.TargetPort)
	return bind + " → " + target
}

func hintsView(width int, variants ...[][2]string) string {
	for _, variant := range variants {
		rendered := renderHints(variant...)
		if lipgloss.Width(rendered) <= width {
			return rendered
		}
	}
	return lipgloss.NewStyle().Foreground(muted).Render(truncate("Esc назад", width))
}
