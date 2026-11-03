package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestTerminalScreenCursorAndErase(t *testing.T) {
	s := newTerminalScreen(8, 3)
	s.Feed([]byte("hello\rXY\nworld"))
	if got := s.String(); got != "XYllo\n  world\n" {
		t.Fatalf("screen:\n%q", got)
	}
	s.Feed([]byte("\x1b[2J\x1b[Hok"))
	if got := s.String(); got != "ok\n\n" {
		t.Fatalf("cleared screen: %q", got)
	}
}

func TestTerminalScreenSplitUTF8(t *testing.T) {
	s := newTerminalScreen(8, 1)
	b := []byte("я")
	s.Feed(b[:1])
	s.Feed(b[1:])
	if got := s.String(); got != "я" {
		t.Fatalf("screen = %q", got)
	}
}

func TestTerminalScreenInvalidUTF8DoesNotBlockFollowingText(t *testing.T) {
	s := newTerminalScreen(8, 1)
	s.Feed([]byte{0xff, 'o', 'k'})
	if got := s.String(); got != "�ok" {
		t.Fatalf("screen = %q", got)
	}
}

func TestTerminalScreenPreservesANSIStyles(t *testing.T) {
	s := newTerminalScreen(4, 1)
	s.Feed([]byte("\x1b[1;38;5;196;48;2;1;2;3mX\x1b[0mY"))
	style := s.rows[0][0].style
	if !style.bold || style.fg != (terminalColor{mode: 1, a: 196}) || style.bg != (terminalColor{mode: 2, a: 1, b: 2, c: 3}) {
		t.Fatalf("style was not preserved: %#v", style)
	}
	rendered := s.RenderANSI()
	if !strings.Contains(rendered, "38;5;196") || !strings.Contains(rendered, "48;2;1;2;3") {
		t.Fatalf("rendered ANSI is missing colors: %q", rendered)
	}
	if width := lipgloss.Width(rendered); width != 4 {
		t.Fatalf("rendered width = %d, want 4", width)
	}
}

func TestTerminalScreenAlternateBuffer(t *testing.T) {
	s := newTerminalScreen(8, 2)
	s.Feed([]byte("primary\x1b[?1049hhtop"))
	if !s.altActive || !strings.HasPrefix(s.String(), "htop") {
		t.Fatalf("alternate screen not entered: %q", s.String())
	}
	s.Feed([]byte("\x1b[?1049l"))
	if s.altActive || !strings.HasPrefix(s.String(), "primary") {
		t.Fatalf("primary screen not restored: %q", s.String())
	}
}

func TestTerminalScreenRepliesToCursorQuery(t *testing.T) {
	s := newTerminalScreen(8, 2)
	s.Feed([]byte("\x1b[2;3H\x1b[6n"))
	replies := s.DrainResponses()
	if len(replies) != 1 || string(replies[0]) != "\x1b[2;3R" {
		t.Fatalf("cursor replies = %q", replies)
	}
}

func TestTerminalScreenDECLineDrawingCharset(t *testing.T) {
	s := newTerminalScreen(8, 1)
	s.Feed([]byte("\x1b(B\x1b)0\x0elqqk\x0fok"))
	if got := s.String(); got != "┌──┐ok" {
		t.Fatalf("line drawing = %q", got)
	}
}

func TestTerminalScreenIgnoresDCS(t *testing.T) {
	s := newTerminalScreen(8, 1)
	s.Feed([]byte("a\x1bPignored\x1b\\b"))
	if got := s.String(); got != "ab" {
		t.Fatalf("DCS leaked into the screen: %q", got)
	}
}

func TestTerminalTracksOSC7CurrentDirectory(t *testing.T) {
	s := newTerminalScreen(40, 4)
	s.SetCurrentDirectory("/home/alice")
	s.Feed([]byte("\x1b]7;file://server/var/www/My%20Site\x07"))
	if got := s.CurrentDirectory(); got != "/var/www/My Site" {
		t.Fatalf("current directory = %q", got)
	}
	s.Feed([]byte("\x1b]7;not-a-file-uri\x07"))
	if got := s.CurrentDirectory(); got != "/var/www/My Site" {
		t.Fatalf("current directory after invalid OSC = %q", got)
	}
}

func TestTerminalPasteUsesRemoteBracketedPasteMode(t *testing.T) {
	s := newTerminalScreen(20, 2)
	if got := string(s.PasteBytes("echo hi")); got != "echo hi" {
		t.Fatalf("plain paste = %q", got)
	}
	s.Feed([]byte("\x1b[?2004h"))
	if got := string(s.PasteBytes("echo hi")); got != "\x1b[200~echo hi\x1b[201~" {
		t.Fatalf("bracketed paste = %q", got)
	}
}

func TestTerminalApplicationCursorModeEncodesArrowKeysForFullScreenApps(t *testing.T) {
	s := newTerminalScreen(20, 2)
	up := tea.KeyPressMsg(tea.Key{Code: tea.KeyUp})
	down := tea.KeyPressMsg(tea.Key{Code: tea.KeyDown})
	if got := string(s.KeyBytes(up)); got != "\x1b[A" {
		t.Fatalf("normal cursor up = %q", got)
	}

	s.Feed([]byte("\x1b[?1h"))
	if !s.applicationCursor {
		t.Fatal("DECCKM application cursor mode was not enabled")
	}
	if got := string(s.KeyBytes(up)); got != "\x1bOA" {
		t.Fatalf("application cursor up = %q", got)
	}
	if got := string(s.KeyBytes(down)); got != "\x1bOB" {
		t.Fatalf("application cursor down = %q", got)
	}

	s.Feed([]byte("\x1b[?1l"))
	if s.applicationCursor || string(s.KeyBytes(up)) != "\x1b[A" {
		t.Fatal("DECCKM application cursor mode was not disabled")
	}
}

func TestTerminalSelectionExtractsAndHighlightsText(t *testing.T) {
	s := newTerminalScreen(8, 2)
	s.Feed([]byte("hello\r\nworld"))
	selection := &terminalSelection{anchor: terminalPoint{x: 1, y: 0}, head: terminalPoint{x: 2, y: 1}}
	if got := s.SelectionText(selection); got != "ello\nwor" {
		t.Fatalf("selected text = %q", got)
	}
	reversed := &terminalSelection{anchor: selection.head, head: selection.anchor}
	if got := s.SelectionText(reversed); got != "ello\nwor" {
		t.Fatalf("reverse selected text = %q", got)
	}
	if rendered := s.RenderANSISelection(selection); !strings.Contains(rendered, "48;2;125;86;244") {
		t.Fatalf("selection highlight is missing: %q", rendered)
	}
}

func TestTerminalScrollbackKeepsHistoryAndAnchorsViewport(t *testing.T) {
	s := newTerminalScreen(8, 3)
	s.Feed([]byte("one\r\ntwo\r\nthree\r\nfour"))
	if got := s.String(); got != "two\nthree\nfour" {
		t.Fatalf("bottom view = %q", got)
	}
	if !s.ScrollLines(1) || s.ScrollOffset() != 1 {
		t.Fatalf("scroll offset = %d", s.ScrollOffset())
	}
	if got := s.String(); got != "one\ntwo\nthree" {
		t.Fatalf("history view = %q", got)
	}

	// Output received while reading history must not move the viewport.
	s.Feed([]byte("\r\nfive"))
	if got := s.String(); got != "one\ntwo\nthree" {
		t.Fatalf("anchored history view = %q", got)
	}
	if s.ScrollOffset() != 2 {
		t.Fatalf("anchored offset = %d", s.ScrollOffset())
	}
	if !s.ScrollToBottom() || s.String() != "three\nfour\nfive" {
		t.Fatalf("bottom after reset = %q", s.String())
	}
}

func TestTerminalScrollbackSelectionUsesVisibleHistory(t *testing.T) {
	s := newTerminalScreen(8, 2)
	s.Feed([]byte("first\r\nsecond\r\nthird"))
	s.ScrollLines(1)
	selection := &terminalSelection{anchor: terminalPoint{x: 0, y: 0}, head: terminalPoint{x: 4, y: 0}, moved: true}
	if got := s.SelectionText(selection); got != "first" {
		t.Fatalf("history selection = %q", got)
	}
}

func TestTerminalAlternateScreenDoesNotPolluteScrollback(t *testing.T) {
	s := newTerminalScreen(8, 2)
	s.Feed([]byte("one\r\ntwo\r\nthree"))
	historyBefore := s.history.Len()
	s.Feed([]byte("\x1b[?1049h1\r\n2\r\n3\r\n4"))
	if s.ScrollLines(1) {
		t.Fatal("alternate screen unexpectedly scrolled primary history")
	}
	s.Feed([]byte("\x1b[?1049l"))
	if s.history.Len() != historyBefore {
		t.Fatalf("history length = %d, want %d", s.history.Len(), historyBefore)
	}
}

func TestTerminalCSI3ClearsScrollbackOnly(t *testing.T) {
	s := newTerminalScreen(8, 2)
	s.Feed([]byte("one\r\ntwo\r\nthree"))
	if s.history.Len() == 0 {
		t.Fatal("test setup did not create history")
	}
	s.Feed([]byte("\x1b[3J"))
	if s.history.Len() != 0 || s.ScrollOffset() != 0 {
		t.Fatalf("history after CSI 3 J: lines=%d offset=%d", s.history.Len(), s.ScrollOffset())
	}
	if got := s.String(); got != "two\nthree" {
		t.Fatalf("CSI 3 J cleared visible screen: %q", got)
	}
}

func TestTerminalResizeKeepsRowsLostFromViewportInHistory(t *testing.T) {
	s := newTerminalScreen(8, 3)
	s.Feed([]byte("one\r\ntwo\r\nthree"))
	s.Resize(8, 2)
	if !s.ScrollLines(1) || s.String() != "one\ntwo" {
		t.Fatalf("history after resize = %q, offset=%d", s.String(), s.ScrollOffset())
	}
}

func TestTerminalEndSessionLeavesFullScreenModesWithoutClearingPrimaryScreen(t *testing.T) {
	s := newTerminalScreen(16, 3)
	s.Feed([]byte("shell prompt"))
	s.Feed([]byte("\x1b[?1049h\x1b[?1h\x1b[?2004hfull screen"))
	if !s.altActive || !s.applicationCursor || !s.bracketedPaste {
		t.Fatal("test setup did not enable full-screen terminal modes")
	}
	s.endSession()
	if s.altActive || s.applicationCursor || s.bracketedPaste {
		t.Fatalf("full-screen modes remained enabled: alt=%v cursor=%v paste=%v", s.altActive, s.applicationCursor, s.bracketedPaste)
	}
	if !strings.Contains(s.String(), "shell prompt") {
		t.Fatalf("primary screen was discarded: %q", s.String())
	}
}

func TestParseCSIArgs(t *testing.T) {
	cases := []struct {
		params string
		sgr    bool
		want   []int
	}{
		{"", false, []int{0}},
		{"5;10", false, []int{5, 10}},
		{";7", false, []int{0, 7}},
		{"1:2", false, []int{0}},
		{"38:2::10:20:30", true, []int{38, 2, 10, 20, 30}},
		{"38;5;196", true, []int{38, 5, 196}},
		{"x;3", false, []int{0, 3}},
	}
	for _, tc := range cases {
		got := parseCSIArgs(nil, tc.params, tc.sgr)
		if fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("parseCSIArgs(%q, %v) = %v, want %v", tc.params, tc.sgr, got, tc.want)
		}
	}
}

func TestStyleANSI(t *testing.T) {
	style := terminalStyle{bold: true, inverse: true, fg: indexedColor(196), bg: terminalColor{mode: 2, a: 1, b: 22, c: 255}}
	if got, want := styleANSI(style), "\x1b[0;1;7;38;5;196;48;2;1;22;255m"; got != want {
		t.Fatalf("styleANSI = %q, want %q", got, want)
	}
	if got := styleANSI(terminalStyle{}); got != "\x1b[0m" {
		t.Fatalf("plain styleANSI = %q", got)
	}
}

func TestCoalesceOutputMergesPendingChunks(t *testing.T) {
	output := make(chan []byte, 4)
	output <- []byte("b")
	output <- []byte("c")
	close(output)
	if got := string(coalesceOutput([]byte("a"), output)); got != "abc" {
		t.Fatalf("coalesced = %q, want abc", got)
	}
	if _, ok := <-output; ok {
		t.Fatal("closed channel must still report closure to the next reader")
	}
}

func TestRepeatPreviousCharacter(t *testing.T) {
	// ncurses (nano, mc) fills gaps with "<char> CSI n-1 b".
	s := newTerminalScreen(20, 2)
	s.Feed([]byte("^K Cut \x1b[5b^T Exec"))
	if got := strings.Split(s.String(), "\n")[0]; got != "^K Cut      ^T Exec" {
		t.Fatalf("REP line = %q", got)
	}
	s.Feed([]byte("\x1b[2;5H\x1b[Zx\x1b[Iy"))
	if got := strings.Split(s.String(), "\n")[1]; got != "x       y" {
		t.Fatalf("tab line = %q", got)
	}
}

func TestKeyBytesForAltAndControlPunctuation(t *testing.T) {
	cases := []struct {
		key  tea.Key
		want string
	}{
		{tea.Key{Code: 'u', Mod: tea.ModAlt}, "\x1bu"},                  // nano undo
		{tea.Key{Code: 'u', Mod: tea.ModAlt | tea.ModShift}, "\x1bU"},   // shifted Alt
		{tea.Key{Code: 'x', Mod: tea.ModAlt | tea.ModCtrl}, "\x1b\x18"}, // Ctrl+Alt+X
		{tea.Key{Code: tea.KeyUp, Mod: tea.ModAlt}, "\x1b[1;3A"},        // Alt+Up
		{tea.Key{Code: '\\', Mod: tea.ModCtrl}, "\x1c"},                 // nano ^\ replace
		{tea.Key{Code: '/', Mod: tea.ModCtrl}, "\x1f"},                  // nano ^/ go to line
		{tea.Key{Code: ']', Mod: tea.ModCtrl}, "\x1d"},
		{tea.Key{Code: tea.KeySpace, Mod: tea.ModCtrl}, "\x00"}, // set mark
		{tea.Key{Code: 'w', Mod: tea.ModCtrl}, "\x17"},
	}
	for _, tc := range cases {
		if got := string(keyBytesForMode(tea.KeyPressMsg(tc.key), false)); got != tc.want {
			t.Errorf("%v: got %q, want %q", tea.KeyPressMsg(tc.key), got, tc.want)
		}
	}
}
