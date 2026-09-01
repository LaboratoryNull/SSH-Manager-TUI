package tui

import (
	"fmt"

	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
)

const (
	themeMidnight = "midnight"
	themeAbyss    = "abyss"
	themeSource   = "source"
	themeWin98    = "win98-dark"
)

type themeDefinition struct {
	ID, Name                                  string
	Background, Accent, AccentAlt, AccentWarm string
	Text, Muted, Surface, Border, Contrast    string
	Error, Success, Warning                   string
}

var themes = []themeDefinition{
	{
		ID: themeMidnight, Name: "Midnight",
		Background: "#11111B", Accent: "#7D56F4", AccentAlt: "#5AF7DC", AccentWarm: "#FF75B7",
		Text: "#CDD6F4", Muted: "#7F849C", Surface: "#25233A", Border: "#45475A", Contrast: "#11111B",
		Error: "#F38BA8", Success: "#A6E3A1", Warning: "#F9E2AF",
	},
	{
		ID: themeAbyss, Name: "Deep Ocean",
		Background: "#0B141A", Accent: "#2F81A3", AccentAlt: "#73C5C8", AccentWarm: "#A98BD4",
		Text: "#D7E4E8", Muted: "#78909A", Surface: "#17272F", Border: "#29434C", Contrast: "#071014",
		Error: "#E06C75", Success: "#8FBC8F", Warning: "#E5C07B",
	},
	{
		ID: themeSource, Name: "Steam Source",
		Background: "#171C18", Accent: "#A4A56B", AccentAlt: "#D0C58A", AccentWarm: "#D4863B",
		Text: "#D8D6C4", Muted: "#89907A", Surface: "#30382F", Border: "#596154", Contrast: "#171C18",
		Error: "#C76555", Success: "#A5B66B", Warning: "#D7A849",
	},
	{
		ID: themeWin98, Name: "Windows 98 Dark",
		Background: "#101216", Accent: "#2F6FBB", AccentAlt: "#35B8B0", AccentWarm: "#C2C7CB",
		Text: "#E3E6E8", Muted: "#9AA0A6", Surface: "#292D33", Border: "#7A828A", Contrast: "#FFFFFF",
		Error: "#E57373", Success: "#5FCB8D", Warning: "#E1C66F",
	},
}

func init() {
	applyTheme(themeMidnight)
}

func normalizedThemeID(id string) string {
	for _, theme := range themes {
		if theme.ID == id {
			return id
		}
	}
	return themeMidnight
}

func themeByID(id string) themeDefinition {
	id = normalizedThemeID(id)
	for _, theme := range themes {
		if theme.ID == id {
			return theme
		}
	}
	return themes[0]
}

func applyTheme(id string) {
	theme := themeByID(id)
	background = lipgloss.Color(theme.Background)
	accent = lipgloss.Color(theme.Accent)
	accentAlt = lipgloss.Color(theme.AccentAlt)
	accentWarm = lipgloss.Color(theme.AccentWarm)
	textColor = lipgloss.Color(theme.Text)
	muted = lipgloss.Color(theme.Muted)
	surface = lipgloss.Color(theme.Surface)
	borderMuted = lipgloss.Color(theme.Border)
	contrastText = lipgloss.Color(theme.Contrast)
	errorColor = lipgloss.Color(theme.Error)
	successColor = lipgloss.Color(theme.Success)
	warningColor = lipgloss.Color(theme.Warning)
	borderStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(borderMuted)
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(accentWarm)
	if theme.ID == themeWin98 {
		borderStyle = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(borderMuted)
		titleStyle = lipgloss.NewStyle().Bold(true).Foreground(contrastText).Background(accent).Padding(0, 1)
	}
	subtitleStyle = lipgloss.NewStyle().Foreground(accentAlt)
	selectedStyle = lipgloss.NewStyle().Background(surface).Foreground(textColor).Bold(true)
	keyStyle = lipgloss.NewStyle().Foreground(accentAlt).Background(surface).Padding(0, 1).Bold(true)
	sizeStyle = lipgloss.NewStyle().Foreground(warningColor).Background(surface)
}

func (m *Model) cycleTheme() {
	oldStored := m.config.Theme
	oldID := normalizedThemeID(oldStored)
	nextIndex := 0
	for index, theme := range themes {
		if theme.ID == oldID {
			nextIndex = (index + 1) % len(themes)
			break
		}
	}
	next := themes[nextIndex]
	m.config.Theme = next.ID
	applyTheme(next.ID)
	m.restyleThemeInputs()
	if m.store != nil {
		if err := m.store.Save(m.config); err != nil {
			m.config.Theme = oldStored
			applyTheme(oldID)
			m.restyleThemeInputs()
			m.status = "Не удалось сохранить тему: " + err.Error()
			return
		}
	}
	m.status = fmt.Sprintf("Тема: %s", next.Name)
}

func currentThemeName(id string) string {
	return themeByID(id).Name
}

func themeTextInput(input *textinput.Model) {
	if input == nil {
		return
	}
	styles := input.Styles()
	styles.Focused.Text = lipgloss.NewStyle().Foreground(textColor)
	styles.Focused.Placeholder = lipgloss.NewStyle().Foreground(borderMuted).Italic(true)
	styles.Blurred.Text = lipgloss.NewStyle().Foreground(muted)
	styles.Blurred.Placeholder = lipgloss.NewStyle().Foreground(borderMuted)
	styles.Cursor.Color = accentWarm
	input.SetStyles(styles)
}

func (m *Model) restyleThemeInputs() {
	restyleInputs(m.inputs)
	themeTextInput(m.groupInput)
	if m.profileManager != nil && m.profileManager.editor != nil {
		restyleInputs(m.profileManager.editor.inputs)
	}
	if m.importWizard != nil {
		themeTextInput(m.importWizard.groupInput)
	}
	if m.forwardModal != nil && m.forwardModal.editor != nil {
		restyleInputs(m.forwardModal.editor.inputs)
	}
	if m.sftpDialog != nil && m.sftpDialog.action != sftpActionDelete {
		themeTextInput(&m.sftpDialog.input)
	}
	for _, state := range m.tabs {
		restyleTabInputs(state)
	}
}

func restyleTabInputs(state *tabState) {
	if state == nil {
		return
	}
	restyleInputs(state.inputs)
	themeTextInput(state.groupInput)
	if state.profileManager != nil && state.profileManager.editor != nil {
		restyleInputs(state.profileManager.editor.inputs)
	}
	if state.importWizard != nil {
		themeTextInput(state.importWizard.groupInput)
	}
	if state.forwardModal != nil && state.forwardModal.editor != nil {
		restyleInputs(state.forwardModal.editor.inputs)
	}
	if state.sftpDialog != nil && state.sftpDialog.action != sftpActionDelete {
		themeTextInput(&state.sftpDialog.input)
	}
}

func restyleInputs(inputs []textinput.Model) {
	for index := range inputs {
		themeTextInput(&inputs[index])
	}
}
