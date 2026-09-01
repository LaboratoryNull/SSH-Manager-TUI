package tui

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"ssh-manager-tui/internal/config"
	"ssh-manager-tui/internal/remote"
)

func TestLayoutUsesTwentyPercentForFiles(t *testing.T) {
	m := &Model{width: 100, height: 40}
	files, console, _, terminal, _ := m.layout()
	if files != 20 || console != 80 || terminal != 78 {
		t.Fatalf("layout = files:%d console:%d terminal:%d", files, console, terminal)
	}
}

func TestCtrlSpaceSwitchesConnectedPane(t *testing.T) {
	m := &Model{page: pageConnected, filesFocused: true}
	_, cmd := m.handleConnectedKey(tea.KeyPressMsg(tea.Key{Code: tea.KeySpace, Mod: tea.ModCtrl}))
	if m.filesFocused {
		t.Fatal("Ctrl+Space did not switch focus to the terminal")
	}
	if cmd != nil {
		t.Fatal("Ctrl+Space should not be forwarded to SSH")
	}
	_, _ = m.handleConnectedKey(tea.KeyPressMsg(tea.Key{Code: '@', Mod: tea.ModCtrl}))
	if !m.filesFocused {
		t.Fatal("legacy Ctrl+@ encoding did not switch focus back to SFTP")
	}
}

func TestTransferHotkeysNoLongerNeedShift(t *testing.T) {
	root := &fileNode{Name: "/", Path: "/", Dir: true}
	m := &Model{
		page: pageConnected, filesFocused: true, root: root, visible: []*fileNode{root},
		conn: &remote.Connection{},
	}
	_, downloadCmd := m.handleConnectedKey(tea.KeyPressMsg(tea.Key{Code: 'd', Mod: tea.ModCtrl}))
	if m.picker == nil || m.picker.mode != pickerDownloadDestination || downloadCmd == nil {
		t.Fatal("Ctrl+D did not open the download destination picker")
	}
	m.picker = nil
	_, uploadCmd := m.handleConnectedKey(tea.KeyPressMsg(tea.Key{Code: 'u', Mod: tea.ModCtrl}))
	if m.picker == nil || m.picker.mode != pickerUploadSource || uploadCmd == nil {
		t.Fatal("Ctrl+U did not open the upload source picker")
	}
}

func TestTransferCanBeCancelledWithCtrlX(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Model{page: pageConnected, transferring: true, transferCancel: cancel}
	_, cmd := m.handleConnectedKey(tea.KeyPressMsg(tea.Key{Code: 'x', Mod: tea.ModCtrl}))
	if cmd != nil {
		t.Fatal("cancellation should not start another command")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("transfer context was not cancelled")
	}
	if !strings.Contains(m.status, "Отмена") {
		t.Fatalf("status = %q", m.status)
	}
}

func TestCancelledTransferFinishesWithoutErrorStatus(t *testing.T) {
	updates := make(chan transferProgressMsg)
	m := &Model{
		page: pageConnected, transferring: true, transferCh: updates,
		transferPath: "/tmp/partial", transferErr: context.Canceled,
	}
	_, _ = m.Update(transferProgressMsg{source: updates, final: true, err: context.Canceled})
	if m.transferring || m.transferPath != "" || m.transferErr != nil || m.status != "Передача файлов отменена" {
		t.Fatalf("cancelled state: transferring=%v path=%q err=%v status=%q", m.transferring, m.transferPath, m.transferErr, m.status)
	}
}

func TestSuccessfulTransferShowsMessageAndClearsProgress(t *testing.T) {
	updates := make(chan transferProgressMsg)
	m := &Model{
		page: pageConnected, transferring: true, transferCh: updates,
		transferDone: 50, transferTotal: 100, transferPath: "/tmp/report.txt", transferSpeed: 1024,
	}
	_, _ = m.Update(transferProgressMsg{
		source: updates, final: true, done: 100, total: 100, path: "/tmp/report.txt",
	})
	if m.transferring || m.transferPath != "" || m.transferDone != 0 || m.transferTotal != 0 || m.transferSpeed != 0 {
		t.Fatalf("completed progress was not cleared: %#v", m)
	}
	if m.status != "Скачано: /tmp/report.txt" {
		t.Fatalf("completion status = %q", m.status)
	}
}

func TestProgressShowsCompactNameSpeedAndPercentage(t *testing.T) {
	m := &Model{
		transferring: true, transferPath: "/tmp/verylongfilename.tar.gz",
		transferDone: 50, transferTotal: 100, transferSpeed: 1.5 * 1024 * 1024,
	}
	plain := ansi.Strip(m.progressView(24))
	for _, wanted := range []string{"verylon...ar.gz", "50%", "1.5MiB/s"} {
		if !strings.Contains(plain, wanted) {
			t.Fatalf("progress %q does not contain %q", plain, wanted)
		}
	}
}

func TestSFTPViewKeepsTwoLineProgressInsidePane(t *testing.T) {
	root := &fileNode{Name: "/root", Path: "/root", Dir: true}
	m := &Model{
		width: 100, height: 30, root: root, visible: []*fileNode{root},
		transferring: true, transferPath: "/tmp/verylongfilename.tar.gz",
		transferDone: 50, transferTotal: 100, transferSpeed: 2 * 1024 * 1024,
	}
	view := m.filesView(18, 25)
	if got := lipgloss.Height(view); got != 25 {
		t.Fatalf("SFTP progress height = %d, want 25", got)
	}
	if got := lipgloss.Width(view); got > 18 {
		t.Fatalf("SFTP progress width = %d, want <= 18", got)
	}
}

func TestDropUploadTargetUsesSelectedSFTPDirectory(t *testing.T) {
	root := &fileNode{Name: "home", Path: "/home/root", Dir: true}
	directory := &fileNode{Name: "uploads", Path: "/home/root/uploads", Dir: true}
	file := &fileNode{Name: "old.txt", Path: "/home/root/old.txt"}
	root.Children = []*fileNode{directory, file}
	m := &Model{
		page: pageConnected, filesFocused: true, root: root,
		visible: []*fileNode{root, directory, file}, fileCursor: 1,
		conn: &remote.Connection{Home: "/home/root"},
	}
	if got := m.dropUploadTarget(); got != directory {
		t.Fatalf("directory drop target = %#v", got)
	}
	m.fileCursor = 2
	if got := m.dropUploadTarget(); got != root {
		t.Fatalf("file drop target = %#v, want its parent", got)
	}
}

func TestDropReleaseOverSFTPSelectsDirectoryUnderPointer(t *testing.T) {
	root := &fileNode{Name: "home", Path: "/home/root", Dir: true}
	directory := &fileNode{Name: "uploads", Path: "/home/root/uploads", Dir: true}
	m := &Model{
		width: 100, height: 30, page: pageConnected, root: root,
		visible: []*fileNode{root, directory}, conn: &remote.Connection{Home: "/home/root"},
	}
	_, _ = m.handleMouseRelease(tea.MouseReleaseMsg(tea.Mouse{X: 5, Y: 4, Button: tea.MouseLeft}))
	if !m.filesFocused || m.fileCursor != 1 || m.dropUploadTarget() != directory {
		t.Fatalf("SFTP drop state: focused=%v cursor=%d target=%#v", m.filesFocused, m.fileCursor, m.dropUploadTarget())
	}
}

func TestEscapeThenQDisconnects(t *testing.T) {
	m := &Model{page: pageConnected, filesFocused: true}
	_, cmd := m.handleConnectedKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	if !m.escapePending || cmd == nil {
		t.Fatal("Esc did not start the disconnect sequence")
	}
	_, _ = m.handleConnectedKey(tea.KeyPressMsg(tea.Key{Code: 'q', Text: "q"}))
	if m.page != pageSessions || !strings.Contains(m.status, "Отключено") {
		t.Fatalf("page = %v, status = %q", m.page, m.status)
	}
}

func TestPickerViewFitsWindow(t *testing.T) {
	m := &Model{
		width: 80, height: 24, page: pageConnected, tabID: 1,
		picker: &localPicker{
			mode: pickerUploadSource, path: "/tmp", marked: make(map[string]bool),
			entries: []localEntry{{name: "example.txt", path: "/tmp/example.txt", size: 42}},
		},
	}
	m.tabs = []*tabState{m.captureTab()}
	view := m.viewContent()
	if got := lipgloss.Width(view); got != 80 {
		t.Fatalf("picker width = %d, want 80", got)
	}
	if got := lipgloss.Height(view); got != 24 {
		t.Fatalf("picker height = %d, want 24", got)
	}
	if !strings.Contains(view, "example.txt") {
		t.Fatal("picker does not render local entries")
	}
}

func TestPickerFileSizeBadgeStaysInsideLine(t *testing.T) {
	entry := localEntry{name: strings.Repeat("очень-длинное-имя-", 5) + ".iso", size: 9 << 30}
	line := pickerEntryLine(entry, "📄 ", 24)
	if got := lipgloss.Width(line); got > 24 {
		t.Fatalf("picker line width = %d, want <= 24", got)
	}
	if !strings.Contains(line, "📦") || !strings.Contains(line, "GiB") {
		t.Fatalf("picker line has no visible size badge: %q", line)
	}
}

func TestStyledTerminalViewFitsWindow(t *testing.T) {
	m := &Model{width: 100, height: 30, page: pageConnected, tabID: 1, profile: config.Session{User: "root", Host: "host"}}
	_, _, _, termWidth, termHeight := m.layout()
	m.term = newTerminalScreen(termWidth, termHeight)
	m.term.Feed([]byte("\x1b[1;38;5;42mcolored prompt\x1b[0m"))
	m.root = &fileNode{Name: "/", Path: "/", Dir: true}
	m.visible = []*fileNode{m.root}
	m.tabs = []*tabState{m.captureTab()}
	view := m.viewContent()
	if got := lipgloss.Width(view); got != 100 {
		t.Fatalf("connected width = %d, want 100", got)
	}
	if got := lipgloss.Height(view); got != 30 {
		t.Fatalf("connected height = %d, want 30", got)
	}
}

func TestTabsPreserveConnectedWorkspace(t *testing.T) {
	firstTerm := newTerminalScreen(60, 20)
	m := &Model{
		width: 100, height: 30, tabID: 1, nextTabID: 1, page: pageConnected,
		profile: config.Session{Name: "production", Host: "prod.example", User: "root"},
		term:    firstTerm, filesFocused: true,
	}
	m.tabs = []*tabState{m.captureTab()}
	m.openTab()
	if len(m.tabs) != 2 || m.page != pageSessions || m.activeTab != 1 {
		t.Fatalf("new tab state: tabs=%d page=%v active=%d", len(m.tabs), m.page, m.activeTab)
	}
	_, _, handled := m.handleTabKey(tea.KeyPressMsg(tea.Key{Code: '1', Mod: tea.ModAlt}))
	if !handled || m.activeTab != 0 || m.page != pageConnected || m.term != firstTerm {
		t.Fatal("Alt+1 did not restore the connected workspace")
	}
}

func TestCloseTabReturnsToRemainingTab(t *testing.T) {
	m := &Model{width: 80, height: 24, tabID: 1, nextTabID: 1, page: pageSessions, filesFocused: true}
	m.resetForm()
	m.tabs = []*tabState{m.captureTab()}
	m.openTab()
	m.closeActiveTab()
	if len(m.tabs) != 1 || m.activeTab != 0 || m.tabID != 1 {
		t.Fatalf("close tab state: tabs=%d active=%d id=%d", len(m.tabs), m.activeTab, m.tabID)
	}
}

func TestCtrlBTogglesSFTPPanel(t *testing.T) {
	m := &Model{width: 100, height: 30, page: pageConnected, filesFocused: true}
	_, _, handled := m.handleTabKey(tea.KeyPressMsg(tea.Key{Code: 'b', Mod: tea.ModCtrl}))
	files, console, _, terminal, _ := m.layout()
	if !handled || !m.sftpHidden || m.filesFocused || files != 0 || console != 100 || terminal != 98 {
		t.Fatalf("hidden layout = hidden:%v focused:%v files:%d console:%d terminal:%d", m.sftpHidden, m.filesFocused, files, console, terminal)
	}
}

func TestCtrlBFocusesSFTPWhenPanelOpens(t *testing.T) {
	m := &Model{width: 100, height: 30, page: pageConnected, sftpHidden: true, filesFocused: false}
	_, _, handled := m.handleTabKey(tea.KeyPressMsg(tea.Key{Code: 'b', Mod: tea.ModCtrl}))
	if !handled || m.sftpHidden || !m.filesFocused {
		t.Fatalf("opened SFTP state = hidden:%v focused:%v", m.sftpHidden, m.filesFocused)
	}
	_, _, _ = m.handleTabKey(tea.KeyPressMsg(tea.Key{Code: 'b', Mod: tea.ModCtrl}))
	if !m.sftpHidden || m.filesFocused {
		t.Fatalf("closed SFTP state = hidden:%v focused:%v", m.sftpHidden, m.filesFocused)
	}
}

func TestSFTPRootCanNavigateToParentAndFilesystemRoot(t *testing.T) {
	root := &fileNode{Name: "/root", Path: "/root", Dir: true}
	m := &Model{
		page: pageConnected, filesFocused: true, conn: &remote.Connection{},
		root: root, visible: []*fileNode{root},
	}
	if cmd := m.collapseSelected(); cmd == nil {
		t.Fatal("moving to parent did not start a directory load")
	}
	if m.root.Path != "/" || m.fileCursor != 0 {
		t.Fatalf("parent root = %q, cursor = %d", m.root.Path, m.fileCursor)
	}
	m.openRemoteDirectory("/opt")
	if m.root.Path != "/opt" {
		t.Fatalf("opened directory = %q", m.root.Path)
	}
}

func TestDirectoryReloadsWhenCollapsedAndExpanded(t *testing.T) {
	node := &fileNode{Name: "opt", Path: "/opt", Dir: true, Expanded: true, Loaded: true}
	m := &Model{
		page: pageConnected, filesFocused: true, conn: &remote.Connection{},
		root: node, visible: []*fileNode{node},
	}
	if cmd := m.collapseSelected(); cmd == nil || node.Expanded || !node.Loading || node.Loaded {
		t.Fatalf("collapse state: cmd=%v expanded=%v loading=%v loaded=%v", cmd != nil, node.Expanded, node.Loading, node.Loaded)
	}
	if cmd := m.expandSelected(); cmd == nil || !node.Expanded || !node.Loading || node.Loaded {
		t.Fatalf("expand state: cmd=%v expanded=%v loading=%v loaded=%v", cmd != nil, node.Expanded, node.Loading, node.Loaded)
	}
	if node.loadID != 2 {
		t.Fatalf("load generation = %d, want 2", node.loadID)
	}
}

func TestMouseClickSwitchesConnectedPane(t *testing.T) {
	m := &Model{width: 100, height: 30, page: pageConnected, sftpHidden: false}
	_, _ = m.handleMouseClick(tea.MouseClickMsg(tea.Mouse{X: 5, Y: 5, Button: tea.MouseLeft}))
	if !m.filesFocused {
		t.Fatal("click in SFTP pane did not focus it")
	}
	_, _ = m.handleMouseClick(tea.MouseClickMsg(tea.Mouse{X: 60, Y: 5, Button: tea.MouseLeft}))
	if m.filesFocused {
		t.Fatal("click in terminal pane did not focus it")
	}
}

func TestMouseDragSelectsTerminalText(t *testing.T) {
	m := &Model{width: 100, height: 30, page: pageConnected, sftpHidden: true, term: newTerminalScreen(98, 25)}
	m.term.Feed([]byte("hello\r\nworld"))
	_, _ = m.handleMouseClick(tea.MouseClickMsg(tea.Mouse{X: 2, Y: 3, Button: tea.MouseLeft}))
	_, _ = m.handleMouseMotion(tea.MouseMotionMsg(tea.Mouse{X: 3, Y: 4, Button: tea.MouseLeft}))
	_, copyCmd := m.handleMouseRelease(tea.MouseReleaseMsg(tea.Mouse{X: 3, Y: 4, Button: tea.MouseLeft}))
	if m.selection == nil || m.selection.dragging {
		t.Fatalf("selection state = %#v", m.selection)
	}
	if got := m.selection.text; got != "ello\nwor" {
		t.Fatalf("selected text = %q", got)
	}
	if copyCmd == nil || m.status != "Копирование текста в системный буфер…" {
		t.Fatalf("copy state: cmd=%v status=%q", copyCmd != nil, m.status)
	}
	_, _ = m.handleClipboardWriteResult(clipboardWriteResultMsg{tabID: m.tabID, text: m.selection.text})
	if m.status != "Текст скопирован в системный буфер" {
		t.Fatalf("copy result status = %q", m.status)
	}
}

func TestShiftInsertRequestsSystemClipboard(t *testing.T) {
	m := &Model{page: pageConnected, sftpHidden: true, term: newTerminalScreen(20, 4)}
	_, cmd := m.handleConnectedKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyInsert, Mod: tea.ModShift}))
	if cmd == nil || !m.clipboardPaste {
		t.Fatalf("paste request: cmd=%v pending=%v", cmd != nil, m.clipboardPaste)
	}
}

func TestRightClickInTerminalRequestsSystemClipboard(t *testing.T) {
	m := &Model{
		width: 100, height: 30, page: pageConnected, sftpHidden: true,
		filesFocused: true, term: newTerminalScreen(98, 25),
	}
	_, clickCmd := m.handleMouseClick(tea.MouseClickMsg(tea.Mouse{X: 2, Y: 3, Button: tea.MouseRight}))
	if clickCmd != nil || !m.rightPasteDown || m.filesFocused {
		t.Fatalf("right press: cmd=%v down=%v filesFocused=%v", clickCmd != nil, m.rightPasteDown, m.filesFocused)
	}
	_, pasteCmd := m.handleMouseRelease(tea.MouseReleaseMsg(tea.Mouse{X: 2, Y: 3, Button: tea.MouseRight}))
	if pasteCmd == nil || !m.clipboardPaste || m.rightPasteDown {
		t.Fatalf("right release: cmd=%v pending=%v down=%v", pasteCmd != nil, m.clipboardPaste, m.rightPasteDown)
	}
}

func TestRightReleaseWithoutPressRequestsSystemClipboard(t *testing.T) {
	m := &Model{width: 100, height: 30, page: pageConnected, sftpHidden: true, term: newTerminalScreen(98, 25)}
	_, cmd := m.handleMouseRelease(tea.MouseReleaseMsg(tea.Mouse{X: 2, Y: 3, Button: tea.MouseRight}))
	if cmd == nil || !m.clipboardPaste {
		t.Fatalf("release-only paste: cmd=%v pending=%v", cmd != nil, m.clipboardPaste)
	}
}

func TestClipboardWriteFailureDoesNotClaimSuccess(t *testing.T) {
	m := &Model{}
	_, fallback := m.handleClipboardWriteResult(clipboardWriteResultMsg{text: "hello", err: errors.New("unavailable")})
	if fallback == nil || strings.Contains(strings.ToLower(m.status), "скопирован в системный") {
		t.Fatalf("fallback state: cmd=%v status=%q", fallback != nil, m.status)
	}
}

func TestSFTPHintIsClickable(t *testing.T) {
	root := &fileNode{Name: "/root", Path: "/root", Dir: true, Expanded: true, Loaded: true}
	m := &Model{
		width: 100, height: 30, page: pageConnected, tabID: 1, sftpHidden: false,
		filesFocused: true, conn: &remote.Connection{}, root: root, visible: []*fileNode{root},
		profile: config.Session{Name: "test", User: "root", Host: "host"}, term: newTerminalScreen(78, 25),
	}
	m.tabs = []*tabState{m.captureTab()}
	x, y := -1, -1
	for _, candidateY := range []int{m.height - 4, m.height - 3, m.height - 2} {
		for candidateX := 0; candidateX < m.width; candidateX++ {
			if m.clickedHint(candidateX, candidateY) == "F5" {
				x, y = candidateX, candidateY
				break
			}
		}
		if x >= 0 {
			break
		}
	}
	if x < 0 {
		t.Fatal("F5 hint has no mouse hit target")
	}
	_, cmd := m.handleMouseClick(tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft}))
	if cmd == nil || !root.Loading || root.Loaded {
		t.Fatalf("clicked reload state: cmd=%v loading=%v loaded=%v", cmd != nil, root.Loading, root.Loaded)
	}
}

func TestSFTPActionHotkeysOpenDialogs(t *testing.T) {
	root := &fileNode{Name: "root", Path: "/root", Dir: true}
	file := &fileNode{Name: "report.txt", Path: "/root/report.txt"}
	root.Children = []*fileNode{file}
	m := &Model{
		width: 100, height: 30, page: pageConnected, filesFocused: true,
		conn: &remote.Connection{}, root: root, visible: []*fileNode{root, file}, fileCursor: 1,
	}
	tests := []struct {
		key    tea.Key
		action sftpAction
	}{
		{tea.Key{Code: 'n', Text: "n"}, sftpActionMkdir},
		{tea.Key{Code: 'c', Text: "c"}, sftpActionCopy},
		{tea.Key{Code: 'm', Text: "m"}, sftpActionMove},
		{tea.Key{Code: 'd', Text: "d"}, sftpActionDelete},
		{tea.Key{Code: tea.KeyDelete}, sftpActionDelete},
	}
	for _, test := range tests {
		m.sftpDialog = nil
		_, _ = m.handleConnectedKey(tea.KeyPressMsg(test.key))
		if m.sftpDialog == nil || m.sftpDialog.action != test.action {
			t.Fatalf("key %q opened dialog %#v, want action %v", test.key.Keystroke(), m.sftpDialog, test.action)
		}
	}
}

func TestSFTPDeleteHintIsClickable(t *testing.T) {
	root := &fileNode{Name: "root", Path: "/root", Dir: true, Expanded: true, Loaded: true}
	file := &fileNode{Name: "report.txt", Path: "/root/report.txt"}
	root.Children = []*fileNode{file}
	m := &Model{
		width: 100, height: 30, page: pageConnected, tabID: 1, sftpHidden: false,
		filesFocused: true, conn: &remote.Connection{}, root: root, visible: []*fileNode{root, file}, fileCursor: 1,
		profile: config.Session{Name: "test", User: "root", Host: "host"}, term: newTerminalScreen(78, 25),
	}
	m.tabs = []*tabState{m.captureTab()}
	foundX, foundY := -1, -1
	for y := m.height - 4; y <= m.height-2 && foundX < 0; y++ {
		for x := 0; x < m.width; x++ {
			if m.clickedHint(x, y) == "d/Del" {
				foundX, foundY = x, y
				break
			}
		}
	}
	if foundX < 0 {
		t.Fatal("delete hint has no mouse hit target")
	}
	_, _ = m.handleMouseClick(tea.MouseClickMsg(tea.Mouse{X: foundX, Y: foundY, Button: tea.MouseLeft}))
	if m.sftpDialog == nil || m.sftpDialog.action != sftpActionDelete {
		t.Fatalf("clicked delete hint opened %#v", m.sftpDialog)
	}
}

func TestSFTPDestinationValidation(t *testing.T) {
	if _, err := sftpDestinationPath("/opt/data", "/opt/data/archive", true); err == nil {
		t.Fatal("copying a directory into its descendant was accepted")
	}
	if _, err := sftpDestinationPath("/opt/report.txt", "/opt", false); err == nil {
		t.Fatal("copying a file onto itself was accepted")
	}
	if got, err := sftpDestinationPath("/opt/report.txt", "/tmp", false); err != nil || got != "/tmp/report.txt" {
		t.Fatalf("destination = %q, err=%v", got, err)
	}
}

func TestSFTPDeleteRequiresConfirmationAndProtectsTreeRoot(t *testing.T) {
	root := &fileNode{Name: "root", Path: "/root", Dir: true}
	file := &fileNode{Name: "report.txt", Path: "/root/report.txt"}
	root.Children = []*fileNode{file}
	m := &Model{
		page: pageConnected, filesFocused: true, conn: &remote.Connection{},
		root: root, visible: []*fileNode{root, file},
	}
	if cmd := m.openSFTPAction(sftpActionDelete); cmd != nil || m.sftpDialog != nil {
		t.Fatal("tree root deletion was not rejected")
	}
	m.fileCursor = 1
	if cmd := m.openSFTPAction(sftpActionDelete); cmd == nil || m.sftpDialog == nil {
		t.Fatal("delete confirmation was not opened")
	}
	_, cmd := m.handleSFTPActionKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd == nil || !m.sftpBusy || m.sftpDialog != nil {
		t.Fatalf("confirmed deletion: cmd=%v busy=%v dialog=%#v", cmd != nil, m.sftpBusy, m.sftpDialog)
	}
}

func TestSessionHintDescriptionIsClickable(t *testing.T) {
	profile := config.Session{Name: "prod", Host: "host", Port: 22, User: "root"}
	m := &Model{
		width: 100, height: 30, page: pageSessions, tabID: 1,
		config: config.Config{Sessions: []config.Session{profile}},
	}
	m.resetForm()
	m.tabs = []*tabState{m.captureTab()}
	foundX, foundY := -1, -1
	for y := 1; y < m.height && foundX < 0; y++ {
		for x := 0; x < m.width; x++ {
			if m.clickedHint(x, y) == "n" {
				foundX, foundY = x, y
				break
			}
		}
	}
	if foundX < 0 {
		t.Fatal("new-session hint has no mouse hit target")
	}
	_, _ = m.handleMouseClick(tea.MouseClickMsg(tea.Mouse{X: foundX, Y: foundY, Button: tea.MouseLeft}))
	if m.page != pageForm {
		t.Fatalf("clicked new-session hint left page at %v", m.page)
	}
}

func TestQOnHostPickerClosesOnlyCurrentTab(t *testing.T) {
	m := &Model{width: 80, height: 24, tabID: 1, nextTabID: 1, page: pageSessions, sftpHidden: true}
	m.resetForm()
	m.tabs = []*tabState{m.captureTab()}
	m.openTab()
	_, cmd := m.handleSessionKey(tea.KeyPressMsg(tea.Key{Code: 'q', Text: "q"}))
	if cmd != nil || len(m.tabs) != 1 || m.activeTab != 0 || m.tabID != 1 {
		t.Fatalf("q result: cmd=%v tabs=%d active=%d id=%d", cmd != nil, len(m.tabs), m.activeTab, m.tabID)
	}
}

func TestSessionsAreGroupedAndOrderedByConnectionCount(t *testing.T) {
	m := &Model{config: config.Config{Sessions: []config.Session{
		{Name: "rare", Group: "Production", ConnectionCount: 1},
		{Name: "ungrouped", ConnectionCount: 2},
		{Name: "frequent", Group: "Production", ConnectionCount: 9},
		{Name: "stage", Group: "Staging", ConnectionCount: 20},
	}}}
	order := m.sessionOrder()
	want := []int{1, 2, 0, 3}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("session order = %v, want %v", order, want)
	}
	if order[1] != 2 || order[2] != 0 {
		t.Fatal("frequent host was not placed above rare host in its group")
	}
}

func TestGroupEditorMovesHostAndPersists(t *testing.T) {
	store := config.NewStoreAt(filepath.Join(t.TempDir(), "config.json"))
	m := &Model{store: store, width: 80, config: config.Config{Sessions: []config.Session{{Name: "prod", Host: "host", Port: 22, User: "root"}}}}
	m.beginGroupEdit()
	if m.groupInput == nil {
		t.Fatal("group editor did not open")
	}
	m.groupInput.SetValue("Production")
	_, _ = m.handleGroupKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Sessions[0].Group; got != "Production" {
		t.Fatalf("saved group = %q", got)
	}
}

func TestGroupEditorOffersAndCompletesExistingGroups(t *testing.T) {
	store := config.NewStoreAt(filepath.Join(t.TempDir(), "config.json"))
	valid := func(name, group string) config.Session {
		return config.Session{Name: name, Host: name + ".example", Port: 22, User: "root", Group: group}
	}
	m := &Model{
		store: store, width: 80, height: 24, selected: 0,
		config: config.Config{Sessions: []config.Session{
			valid("target", ""), valid("prod", "Production"), valid("personal", "Personal"), valid("stage", "Staging"),
		}},
	}
	m.beginGroupEdit()
	m.groupInput.SetValue("pro")
	if suggestions := m.groupSuggestions(); !reflect.DeepEqual(suggestions, []string{"Production"}) {
		t.Fatalf("suggestions = %#v", suggestions)
	}
	_, _ = m.handleGroupKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	_, _ = m.handleGroupKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	if got := m.groupInput.Value(); got != "Production" {
		t.Fatalf("completed group = %q", got)
	}
	_, _ = m.handleGroupKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Sessions[0].Group; got != "Production" {
		t.Fatalf("saved completed group = %q", got)
	}
}

func TestRecordConnectionPersistsFrequency(t *testing.T) {
	store := config.NewStoreAt(filepath.Join(t.TempDir(), "config.json"))
	profile := config.Session{Name: "prod", Host: "host", Port: 22, User: "root", ConnectionCount: 3}
	m := &Model{store: store, config: config.Config{Sessions: []config.Session{profile}}}
	if err := m.recordConnection(profile); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Sessions[0].ConnectionCount; got != 4 {
		t.Fatalf("connection count = %d", got)
	}
}

func TestAsyncMessageUpdatesOwningInactiveTab(t *testing.T) {
	m := &Model{width: 80, height: 24, tabID: 1, nextTabID: 1, page: pageForm, testing: true, filesFocused: true}
	m.resetForm()
	m.testing = true
	m.tabs = []*tabState{m.captureTab()}
	m.openTab()
	wantErr := errors.New("key rejected")
	_, _ = m.Update(keyTestMsg{tabID: 1, err: wantErr})
	if m.activeTab != 1 || m.page != pageSessions || m.status != "" {
		t.Fatal("background result changed the active tab")
	}
	first := m.tabs[0]
	if first.testing || !strings.Contains(first.status, wantErr.Error()) {
		t.Fatalf("inactive tab was not updated: testing=%v status=%q", first.testing, first.status)
	}
}

func TestNewTabsHideSFTPByDefault(t *testing.T) {
	store := config.NewStoreAt(t.TempDir() + "/config.json")
	m, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	if !m.sftpHidden || m.filesFocused {
		t.Fatalf("initial SFTP state = hidden:%v focused:%v", m.sftpHidden, m.filesFocused)
	}
	m.openTab()
	if !m.sftpHidden || m.filesFocused {
		t.Fatalf("new tab SFTP state = hidden:%v focused:%v", m.sftpHidden, m.filesFocused)
	}
}

func TestForwardEditorPersistsDefinition(t *testing.T) {
	store := config.NewStoreAt(t.TempDir() + "/config.json")
	profile := config.Session{Name: "prod", Host: "host", Port: 22, User: "root"}
	m := &Model{
		store: store, config: config.Config{Sessions: []config.Session{profile}}, profile: profile,
		page: pageConnected, forwardModal: &forwardModal{}, forwards: make(map[string]*forwardRuntime),
	}
	editor := newForwardEditor(nil, -1)
	editor.inputs[0].SetValue("postgres")
	editor.inputs[1].SetValue("127.0.0.1")
	editor.inputs[2].SetValue("5433")
	editor.inputs[3].SetValue("database")
	editor.inputs[4].SetValue("5432")
	editor.autoStart = true
	m.forwardModal.editor = editor
	if cmd := m.saveForwardEditor(); cmd != nil {
		t.Fatal("saving without a connection should not start a command")
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Sessions[0].PortForwards) != 1 {
		t.Fatalf("saved forwards = %#v", loaded.Sessions[0].PortForwards)
	}
	got := loaded.Sessions[0].PortForwards[0]
	if got.Name != "postgres" || got.Type != config.ForwardLocal || !got.AutoStart {
		t.Fatalf("saved forward = %#v", got)
	}
}

func TestAltPOpensForwardModal(t *testing.T) {
	m := &Model{page: pageConnected}
	_, _, handled := m.handleTabKey(tea.KeyPressMsg(tea.Key{Code: 'p', Mod: tea.ModAlt}))
	if !handled || m.forwardModal == nil {
		t.Fatal("Alt+P did not open the port forward modal")
	}
}

func TestForwardModalFitsWindow(t *testing.T) {
	profile := config.Session{
		Name: "prod", Host: "host", Port: 22, User: "root",
		PortForwards: []config.PortForward{{
			Name: "postgres", Type: config.ForwardLocal, BindHost: "127.0.0.1", BindPort: 5433,
			TargetHost: "database", TargetPort: 5432, AutoStart: true,
		}},
	}
	m := &Model{
		width: 80, height: 24, tabID: 1, page: pageConnected, profile: profile,
		forwardModal: &forwardModal{}, forwards: make(map[string]*forwardRuntime), sftpHidden: true,
	}
	m.tabs = []*tabState{m.captureTab()}
	view := m.viewContent()
	if got := lipgloss.Width(view); got != 80 {
		t.Fatalf("forward modal width = %d, want 80", got)
	}
	if got := lipgloss.Height(view); got != 24 {
		t.Fatalf("forward modal height = %d, want 24", got)
	}
	if !strings.Contains(view, "postgres") || !strings.Contains(view, "Alt+P") {
		t.Fatal("forward modal does not contain the configured tunnel and shortcut")
	}
}

func TestKeyBytes(t *testing.T) {
	tests := []struct {
		key  tea.KeyPressMsg
		want string
	}{
		{tea.KeyPressMsg(tea.Key{Code: 'я', Text: "я"}), "я"},
		{tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}), "\r"},
		{tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}), "\x1b[A"},
		{tea.KeyPressMsg(tea.Key{Code: 'd', Mod: tea.ModCtrl}), "\x04"},
		{tea.KeyPressMsg(tea.Key{Code: tea.KeyF10}), "\x1b[21~"},
	}
	for _, test := range tests {
		if got := string(keyBytes(test.key)); got != test.want {
			t.Fatalf("key %q = %q, want %q", test.key.String(), got, test.want)
		}
	}
}
