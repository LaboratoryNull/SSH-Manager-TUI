package tui

import (
	"math"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"ssh-manager-tui/internal/config"
)

func TestThemesAreDistinctAndUseDarkBackgrounds(t *testing.T) {
	if len(themes) != 5 {
		t.Fatalf("theme count = %d, want 5", len(themes))
	}
	seen := make(map[string]bool)
	for _, theme := range themes {
		if seen[theme.ID] {
			t.Fatalf("duplicate theme ID %q", theme.ID)
		}
		seen[theme.ID] = true
		if luminance(theme.Background) >= 0.18 {
			t.Fatalf("theme %q background %s is not dark", theme.Name, theme.Background)
		}
		if luminance(theme.Surface) >= 0.18 {
			t.Fatalf("theme %q surface %s is not dark", theme.Name, theme.Surface)
		}
		for role, pair := range map[string][2]string{
			"text/background":  {theme.Text, theme.Background},
			"muted/background": {theme.Muted, theme.Background},
			"text/surface":     {theme.Text, theme.Surface},
			"selected":         {theme.Contrast, theme.Accent},
		} {
			if ratio := contrastRatio(pair[0], pair[1]); ratio < 4 {
				t.Errorf("theme %q %s contrast = %.2f, want at least 4", theme.Name, role, ratio)
			}
		}
	}
	if source := themeByID(themeSource); source.Name != "Steam Source" || source.AccentWarm != "#D4863B" {
		t.Fatalf("Source theme = %#v", source)
	}
	if windows := themeByID(themeWin98); windows.Name != "Windows 98 Dark" || windows.AccentAlt != "#35B8B0" {
		t.Fatalf("Windows 98 theme = %#v", windows)
	}
}

func TestThemeCyclesAndPersistsFromHostScreen(t *testing.T) {
	defer applyTheme(themeMidnight)
	store := config.NewStoreAt(t.TempDir() + "/config.json")
	if err := store.Save(config.Config{Theme: themeMidnight}); err != nil {
		t.Fatal(err)
	}
	m, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	m.openCommandPalette()
	for _, r := range "тем" {
		m.handleKey(tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)}))
	}
	_, cmd := m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd != nil || m.config.Theme != themeAbyss || !strings.Contains(m.status, "Deep Ocean") {
		t.Fatalf("cycled theme: id=%q status=%q cmd nil=%v", m.config.Theme, m.status, cmd == nil)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Theme != themeAbyss {
		t.Fatalf("saved theme = %q", loaded.Theme)
	}
	cycleThemeFromPalette(m)
	if m.config.Theme != themeSource || !strings.Contains(m.status, "Steam Source") {
		t.Fatalf("clickable theme hint did not cycle: id=%q status=%q", m.config.Theme, m.status)
	}
	cycleThemeFromPalette(m)
	if m.config.Theme != themeWin98 || !strings.Contains(m.status, "Windows 98 Dark") {
		t.Fatalf("Windows theme did not cycle: id=%q status=%q", m.config.Theme, m.status)
	}
	cycleThemeFromPalette(m)
	if m.config.Theme != themeCorporate || !strings.Contains(m.status, "Corporate") {
		t.Fatalf("theme cycle did not wrap: id=%q status=%q", m.config.Theme, m.status)
	}
	cycleThemeFromPalette(m)
	if m.config.Theme != themeMidnight || !strings.Contains(m.status, "Midnight") {
		t.Fatalf("theme cycle did not return to Midnight: id=%q status=%q", m.config.Theme, m.status)
	}
}

func cycleThemeFromPalette(m *Model) {
	m.openCommandPalette()
	for _, r := range "тем" {
		m.handleKey(tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)}))
	}
	m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
}

func TestWindows98ThemeUsesSegmentedProgressBar(t *testing.T) {
	defer applyTheme(themeMidnight)
	applyTheme(themeWin98)
	m := &Model{
		config: config.Config{Theme: themeWin98}, transferring: true,
		transferPath: "/tmp/setup.exe", transferDone: 50, transferTotal: 100,
	}
	plain := ansi.Strip(m.progressView(24))
	lines := strings.Split(plain, "\n")
	if len(lines) != 2 || !strings.Contains(lines[1], "[") || !strings.Contains(lines[1], "■ ") || !strings.Contains(lines[1], "]") {
		t.Fatalf("Windows 98 progress bar is not segmented: %q", plain)
	}
	if strings.Contains(lines[1], "█") || strings.Contains(lines[1], "░") {
		t.Fatalf("Windows 98 progress bar uses the default glyphs: %q", lines[1])
	}
}

func TestUnknownStoredThemeFallsBackToCorporate(t *testing.T) {
	defer applyTheme(themeMidnight)
	store := config.NewStoreAt(t.TempDir() + "/config.json")
	if err := store.Save(config.Config{Theme: "removed-theme"}); err != nil {
		t.Fatal(err)
	}
	m, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	if m.config.Theme != themeCorporate {
		t.Fatalf("normalized theme = %q", m.config.Theme)
	}
}

func TestTabBarAndHostScreenStayFreeOfKeyHints(t *testing.T) {
	m := &Model{
		width: 120, height: 30, page: pageSessions, tabID: 1, appVersion: "v1.2.0",
		config: config.Config{Theme: themeMidnight},
	}
	m.resetForm()
	m.tabs = []*tabState{m.captureTab()}

	topLine := strings.TrimSpace(ansi.Strip(m.tabsView()))
	if !strings.HasPrefix(topLine, "SSH Manager") || !strings.HasSuffix(topLine, "sshm v1.2.0") ||
		strings.Contains(topLine, "тема:") || strings.Contains(topLine, "^T") {
		t.Fatalf("app bar should show the product, location and version only: %q", topLine)
	}
	if strings.Contains(ansi.Strip(m.sessionsView()), "тема:") {
		t.Fatal("theme hint is rendered inside the host screen")
	}
}

func luminance(hexColor string) float64 {
	value := strings.TrimPrefix(hexColor, "#")
	if len(value) != 6 {
		return 1
	}
	channel := func(offset int) float64 {
		parsed, err := strconv.ParseUint(value[offset:offset+2], 16, 8)
		if err != nil {
			return 255
		}
		srgb := float64(parsed) / 255
		if srgb <= 0.04045 {
			return srgb / 12.92
		}
		return math.Pow((srgb+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(0) + 0.7152*channel(2) + 0.0722*channel(4)
}

func contrastRatio(first, second string) float64 {
	firstLuminance, secondLuminance := luminance(first), luminance(second)
	if firstLuminance < secondLuminance {
		firstLuminance, secondLuminance = secondLuminance, firstLuminance
	}
	return (firstLuminance + 0.05) / (secondLuminance + 0.05)
}
