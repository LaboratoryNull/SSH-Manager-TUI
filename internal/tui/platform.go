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
			newTab: "⌘T", closeTab: "⌘W", toggleSFTP: "⌘B",
			copy: "⌘C", cancel: "⌘X", focus: "⌘Space",
			download: "⌘D", upload: "⌘U", quit: "⌘Q",
			tabs: "⌥←/→", numberedTabs: "⌥1…9", ports: "⌥P",
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
	if goos != "darwin" {
		keys.newTab, keys.closeTab, keys.toggleSFTP = "^T", "^W", "^B"
		keys.cancel, keys.focus, keys.download, keys.upload = "^X", "^Space", "^D", "^U"
		keys.tabs, keys.ports = "Alt←/→", "AltP"
	}
	return keys
}

func matchesPlatformShortcut(key tea.KeyPressMsg, code rune, shiftedOnNonMac bool) bool {
	return matchesPlatformShortcutForOS(runtime.GOOS, key, code, shiftedOnNonMac)
}

func matchesPlatformShortcutForOS(goos string, key tea.KeyPressMsg, code rune, shiftedOnNonMac bool) bool {
	expected := tea.ModCtrl
	if goos == "darwin" {
		expected = tea.ModSuper
	} else if shiftedOnNonMac {
		expected |= tea.ModShift
	}
	meaningful := key.Mod & (tea.ModCtrl | tea.ModAlt | tea.ModShift | tea.ModMeta | tea.ModHyper | tea.ModSuper)
	actual := key.Code
	if key.BaseCode != 0 {
		actual = key.BaseCode
	}
	return meaningful == expected && unicode.ToLower(actual) == unicode.ToLower(code)
}

func platformCommandMod() tea.KeyMod {
	return platformCommandModForOS(runtime.GOOS)
}

func platformCommandModForOS(goos string) tea.KeyMod {
	if goos == "darwin" {
		return tea.ModSuper
	}
	return tea.ModCtrl
}

func isPlatformHint(label string, values ...string) bool {
	for _, value := range values {
		if strings.EqualFold(label, value) {
			return true
		}
	}
	return false
}
