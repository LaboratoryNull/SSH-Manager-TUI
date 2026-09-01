package tui

import (
	"runtime"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
)

type platformKeys struct {
	newTab, closeTab, toggleSFTP string
	copy, cancel, focus          string
	download, upload, quit       string
	tabs, numberedTabs, ports    string
}

func currentPlatformKeys() platformKeys { return platformKeysForOS(runtime.GOOS) }

func platformKeysForOS(goos string) platformKeys {
	if goos == "darwin" {
		return platformKeys{
			newTab: "Ctrl+T", closeTab: "Ctrl+W", toggleSFTP: "Ctrl+B",
			copy: "Ctrl+Shift+C", cancel: "Ctrl+X", focus: "Ctrl+Shift+Space",
			download: "Ctrl+D", upload: "Ctrl+U", quit: "Ctrl+Q",
			tabs: "Ctrl+Shift+←/→", numberedTabs: "Ctrl+Shift+1…9", ports: "Ctrl+Shift+P",
		}
	}
	return platformKeys{
		newTab: "Ctrl+T", closeTab: "Ctrl+W", toggleSFTP: "Ctrl+B",
		copy: "Ctrl+Shift+C", cancel: "Ctrl+X", focus: "Ctrl+Space",
		download: "Ctrl+D", upload: "Ctrl+U", quit: "Ctrl+Q",
		tabs: "Alt+←/→", numberedTabs: "Alt+1…9", ports: "Alt+P",
	}
}

func compactPlatformKeys() platformKeys {
	return compactPlatformKeysForOS(runtime.GOOS)
}

func compactPlatformKeysForOS(goos string) platformKeys {
	keys := platformKeysForOS(goos)
	keys.newTab, keys.closeTab, keys.toggleSFTP = "^T", "^W", "^B"
	keys.cancel, keys.download, keys.upload = "^X", "^D", "^U"
	keys.copy = "^⇧C"
	if goos == "darwin" {
		keys.focus, keys.tabs, keys.ports = "^⇧Space", "^⇧←/→", "^⇧P"
		keys.numberedTabs = "^⇧1…9"
	} else {
		keys.focus = "^Space"
		keys.tabs, keys.ports = "Alt←/→", "AltP"
	}
	return keys
}

func matchesPlatformShortcut(key tea.KeyPressMsg, code rune, shifted bool) bool {
	return matchesPlatformShortcutForOS(runtime.GOOS, key, code, shifted)
}

func matchesPlatformShortcutForOS(goos string, key tea.KeyPressMsg, code rune, shifted bool) bool {
	expected := platformPrimaryModForOS(goos)
	if shifted {
		expected |= tea.ModShift
	}
	return matchesShortcut(key, code, expected)
}

func matchesPlatformFocusShortcut(key tea.KeyPressMsg) bool {
	return matchesPlatformFocusShortcutForOS(runtime.GOOS, key)
}

func matchesPlatformFocusShortcutForOS(goos string, key tea.KeyPressMsg) bool {
	return matchesShortcut(key, tea.KeySpace, platformFocusModForOS(goos))
}

func matchesPlatformNavigationShortcut(key tea.KeyPressMsg, code rune) bool {
	return matchesPlatformNavigationShortcutForOS(runtime.GOOS, key, code)
}

func matchesPlatformNavigationShortcutForOS(goos string, key tea.KeyPressMsg, code rune) bool {
	return matchesShortcut(key, code, platformNavigationModForOS(goos))
}

func matchesShortcut(key tea.KeyPressMsg, code rune, expected tea.KeyMod) bool {
	meaningful := key.Mod & (tea.ModCtrl | tea.ModAlt | tea.ModShift | tea.ModMeta | tea.ModHyper | tea.ModSuper)
	actual := key.Code
	if key.BaseCode != 0 {
		actual = key.BaseCode
	}
	return meaningful == expected && unicode.ToLower(actual) == unicode.ToLower(code)
}

func platformPrimaryMod() tea.KeyMod {
	return platformPrimaryModForOS(runtime.GOOS)
}

func platformPrimaryModForOS(string) tea.KeyMod {
	return tea.ModCtrl
}

func platformCopyMod() tea.KeyMod { return tea.ModCtrl | tea.ModShift }

func platformFocusMod() tea.KeyMod { return platformFocusModForOS(runtime.GOOS) }

func platformFocusModForOS(goos string) tea.KeyMod {
	if goos == "darwin" {
		return tea.ModCtrl | tea.ModShift
	}
	return tea.ModCtrl
}

func platformNavigationMod() tea.KeyMod { return platformNavigationModForOS(runtime.GOOS) }

func platformNavigationModForOS(goos string) tea.KeyMod {
	if goos == "darwin" {
		return tea.ModCtrl | tea.ModShift
	}
	return tea.ModAlt
}

func isPlatformHint(label string, values ...string) bool {
	for _, value := range values {
		if strings.EqualFold(label, value) {
			return true
		}
	}
	return false
}
