package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"ssh-manager-tui/internal/config"
)

func TestPlatformHintsDifferByOS(t *testing.T) {
	mac := platformKeysForOS("darwin")
	if mac.newTab != "Ctrl+T" || mac.copy != "Ctrl+Shift+C" || mac.focus != "Ctrl+Shift+Space" || mac.tabs != "Ctrl+Shift+←/→" || mac.ports != "Ctrl+Shift+P" {
		t.Fatalf("macOS hints = %#v", mac)
	}
	linux := platformKeysForOS("linux")
	if linux.newTab != "Ctrl+T" || linux.copy != "Ctrl+Shift+C" || linux.tabs != "Alt+←/→" || linux.ports != "Alt+P" {
		t.Fatalf("Linux hints = %#v", linux)
	}
	windows := platformKeysForOS("windows")
	if windows.focus != "Ctrl+Space" || windows.download != "Ctrl+D" || windows.upload != "Ctrl+U" {
		t.Fatalf("Windows hints = %#v", windows)
	}
	if compactPlatformKeysForOS("darwin").tabs != "^⇧←/→" || compactPlatformKeysForOS("linux").newTab != "^T" {
		t.Fatal("compact hints do not follow the target platform")
	}
}

func TestPlatformShortcutsUseControlOnDarwin(t *testing.T) {
	commandT := tea.KeyPressMsg(tea.Key{Code: 'е', BaseCode: 't', Mod: tea.ModSuper})
	controlT := tea.KeyPressMsg(tea.Key{Code: 'е', BaseCode: 't', Mod: tea.ModCtrl})
	if !matchesPlatformShortcutForOS("darwin", controlT, 't', false) {
		t.Fatal("Ctrl+T was not recognized on macOS with a non-Latin layout")
	}
	if matchesPlatformShortcutForOS("darwin", commandT, 't', false) {
		t.Fatal("Command+T is still treated as the macOS application shortcut")
	}
	if !matchesPlatformShortcutForOS("linux", controlT, 't', false) {
		t.Fatal("Ctrl+T was not recognized on Linux")
	}

	controlShiftC := tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl | tea.ModShift})
	if !matchesPlatformShortcutForOS("darwin", controlShiftC, 'c', true) {
		t.Fatal("Ctrl+Shift+C was not recognized as the macOS copy shortcut")
	}
	if !matchesPlatformShortcutForOS("linux", controlShiftC, 'c', true) {
		t.Fatal("Ctrl+Shift+C was not recognized as the Linux copy shortcut")
	}
}

func TestMacNavigationShortcutsAvoidSystemControlAndOptionBindings(t *testing.T) {
	controlShiftRight := tea.KeyPressMsg(tea.Key{Code: tea.KeyRight, Mod: tea.ModCtrl | tea.ModShift})
	controlRight := tea.KeyPressMsg(tea.Key{Code: tea.KeyRight, Mod: tea.ModCtrl})
	optionRight := tea.KeyPressMsg(tea.Key{Code: tea.KeyRight, Mod: tea.ModAlt})
	if !matchesPlatformNavigationShortcutForOS("darwin", controlShiftRight, tea.KeyRight) {
		t.Fatal("Ctrl+Shift+Right was not recognized for macOS tab navigation")
	}
	if matchesPlatformNavigationShortcutForOS("darwin", controlRight, tea.KeyRight) {
		t.Fatal("system Ctrl+Right shortcut was captured on macOS")
	}
	if matchesPlatformNavigationShortcutForOS("darwin", optionRight, tea.KeyRight) {
		t.Fatal("Option+Right is still captured on macOS")
	}
	if !matchesPlatformNavigationShortcutForOS("linux", optionRight, tea.KeyRight) {
		t.Fatal("Alt+Right stopped working on Linux")
	}
	controlShiftOne := tea.KeyPressMsg(tea.Key{Code: '!', BaseCode: '1', Mod: tea.ModCtrl | tea.ModShift})
	if !matchesPlatformNavigationShortcutForOS("darwin", controlShiftOne, '1') {
		t.Fatal("Ctrl+Shift+1 was not recognized from its shifted key code")
	}

	controlSpace := tea.KeyPressMsg(tea.Key{Code: tea.KeySpace, Mod: tea.ModCtrl})
	controlShiftSpace := tea.KeyPressMsg(tea.Key{Code: tea.KeySpace, Mod: tea.ModCtrl | tea.ModShift})
	if matchesPlatformFocusShortcutForOS("darwin", controlSpace) || !matchesPlatformFocusShortcutForOS("darwin", controlShiftSpace) {
		t.Fatal("macOS focus shortcut must be Ctrl+Shift+Space")
	}
	if !matchesPlatformFocusShortcutForOS("windows", controlSpace) || matchesPlatformFocusShortcutForOS("windows", controlShiftSpace) {
		t.Fatal("Windows focus shortcut must be Ctrl+Space")
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
