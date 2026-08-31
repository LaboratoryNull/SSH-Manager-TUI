package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"ssh-manager-tui/internal/config"
)

func TestPlatformHintsUseMacSymbolsOnlyOnDarwin(t *testing.T) {
	mac := platformKeysForOS("darwin")
	if mac.newTab != "⌘T" || mac.copy != "⌘C" || mac.tabs != "⌥←/→" || mac.ports != "⌥P" {
		t.Fatalf("macOS hints = %#v", mac)
	}
	linux := platformKeysForOS("linux")
	if linux.newTab != "Ctrl+T" || linux.copy != "Ctrl+Shift+C" || linux.tabs != "Alt+←/→" || linux.ports != "Alt+P" {
		t.Fatalf("Linux hints = %#v", linux)
	}
	if compactPlatformKeysForOS("darwin").newTab != "⌘T" || compactPlatformKeysForOS("linux").newTab != "^T" {
		t.Fatal("compact hints do not follow the target platform")
	}
}

func TestPlatformShortcutsUseCommandOnDarwin(t *testing.T) {
	commandT := tea.KeyPressMsg(tea.Key{Code: 'е', BaseCode: 't', Mod: tea.ModSuper})
	controlT := tea.KeyPressMsg(tea.Key{Code: 't', Mod: tea.ModCtrl})
	if !matchesPlatformShortcutForOS("darwin", commandT, 't', false) {
		t.Fatal("Command+T was not recognized on macOS with a non-Latin layout")
	}
	if matchesPlatformShortcutForOS("darwin", controlT, 't', false) {
		t.Fatal("Ctrl+T was treated as the macOS application shortcut")
	}
	if !matchesPlatformShortcutForOS("linux", controlT, 't', false) {
		t.Fatal("Ctrl+T was not recognized on Linux")
	}

	commandC := tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModSuper})
	controlShiftC := tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl | tea.ModShift})
	if !matchesPlatformShortcutForOS("darwin", commandC, 'c', true) {
		t.Fatal("Command+C was not recognized as the macOS copy shortcut")
	}
	if !matchesPlatformShortcutForOS("linux", controlShiftC, 'c', true) {
		t.Fatal("Ctrl+Shift+C was not recognized as the Linux copy shortcut")
	}
}

func TestProfileListDoesNotExposeANSICodesAsText(t *testing.T) {
	m := &Model{
		width: 100, height: 30,
		config: config.Config{UserProfiles: []config.UserProfile{{
			Name: "devops", User: "root", Password: "password",
		}}},
		profileManager: &profileManager{},
	}
	plain := ansi.Strip(m.profileManagerView())
	if strings.ContainsRune(plain, '�') || strings.Contains(plain, "[38;2;") {
		t.Fatalf("profile row contains a rendered ANSI sequence: %q", plain)
	}
	if !strings.Contains(plain, "devops  root  пароль") {
		t.Fatalf("profile row is missing clean profile data: %q", plain)
	}
}
