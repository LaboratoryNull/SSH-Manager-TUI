package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"ssh-manager-tui/internal/config"
)

// decodeKey parses raw terminal input the way Bubble Tea does.
func decodeKey(t *testing.T, raw string) tea.KeyPressMsg {
	t.Helper()
	var decoder uv.EventDecoder
	_, event := decoder.Decode([]byte(raw))
	key, ok := event.(uv.KeyPressEvent)
	if !ok {
		t.Fatalf("%q decoded to %T", raw, event)
	}
	return tea.KeyPressMsg(key)
}

// kitty-protocol terminals send Enter with associated text "\r".
var kittyEnter = []string{"\r", "\x1b[13u", "\x1b[13;1;13u", "\x1b[13;129;13u", "\x1b[13;1:1;13u"}

func TestEnterWithAssociatedTextIsEnterEverywhere(t *testing.T) {
	for _, raw := range kittyEnter {
		key := normalizeKeyText(decodeKey(t, raw))
		if key.String() != "enter" {
			t.Fatalf("%q: String() = %q, want enter", raw, key.String())
		}
		if got := string(keyBytesForMode(key, false)); got != "\r" {
			t.Fatalf("%q: terminal bytes = %q", raw, got)
		}
	}
	tab := normalizeKeyText(decodeKey(t, "\x1b[9;1;9u"))
	if tab.String() != "tab" {
		t.Fatalf("tab with text: %q", tab.String())
	}
	if letter := normalizeKeyText(decodeKey(t, "\x1b[97;1;97u")); letter.Text != "a" {
		t.Fatalf("printable text was dropped: %q", letter.Text)
	}
}

func TestKittyEnterConnectsFromLauncherInsteadOfTypingASpace(t *testing.T) {
	for _, raw := range kittyEnter {
		m := &Model{width: 100, height: 30, page: pageSessions, tabID: 1, config: config.Config{Sessions: []config.Session{
			{Name: "prod", Host: "prod.example", Port: 22, User: "deploy"},
		}}}
		m.resetForm()
		m.tabs = []*tabState{m.captureTab()}
		m.Update(decodeKey(t, "p"))
		_, cmd := m.Update(decodeKey(t, raw))
		if cmd == nil || !m.connecting || m.launcherQuery() != "p" {
			t.Fatalf("%q: connecting=%v query=%q", raw, m.connecting, m.launcher.query.Value())
		}
	}
}

func TestKittyEnterRunsPaletteCommand(t *testing.T) {
	m := &Model{width: 100, height: 30, page: pageSessions, tabID: 1}
	m.resetForm()
	m.tabs = []*tabState{m.captureTab()}
	m.Update(decodeKey(t, "\x1b[107;5u")) // Ctrl+K in the kitty protocol
	if m.palette == nil {
		t.Fatal("Ctrl+K did not open the palette")
	}
	for _, raw := range []string{"н", "о", "в"} {
		m.Update(decodeKey(t, raw))
	}
	m.Update(decodeKey(t, "\x1b[13;1;13u"))
	if m.palette != nil || m.page != pageForm {
		t.Fatalf("palette Enter: palette open=%v page=%v", m.palette != nil, m.page)
	}
}
