package tui

import (
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
