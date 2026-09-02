package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	appupdate "ssh-manager-tui/internal/update"
)

func TestReleaseBuildChecksForUpdatesOnStartup(t *testing.T) {
	release := &Model{appVersion: "v1.0.1", updateManifestURL: "https://updates.example/manifest.json"}
	if release.checkForUpdate() == nil {
		t.Fatal("release build did not schedule an update check")
	}
	development := &Model{appVersion: "dev-local", updateManifestURL: "https://updates.example/manifest.json"}
	if development.checkForUpdate() != nil {
		t.Fatal("development build unexpectedly scheduled an update check")
	}
}

func TestAvailableUpdateOpensLargeDialog(t *testing.T) {
	m := &Model{
		appVersion: "v1.0.1", width: 100, height: 30,
		tabs: []*tabState{{page: pageSessions}},
	}
	_, _ = m.handleUpdateCheck(updateCheckMsg{result: appupdate.Result{
		CurrentVersion: "v1.0.1", LatestVersion: "v1.1.0", Available: true,
	}})
	if m.updateDialog == nil {
		t.Fatal("available update did not open the dialog")
	}
	plain := ansi.Strip(m.viewContent())
	for _, wanted := range []string{"sshm v1.0.1", "ДОСТУПНО ОБНОВЛЕНИЕ", "v1.1.0", "Обновить", "Позже"} {
		if !strings.Contains(plain, wanted) {
			t.Fatalf("update dialog does not contain %q:\n%s", wanted, plain)
		}
	}
	if got := m.View(); got.AltScreen != true {
		t.Fatal("update dialog disabled the alternate screen")
	}
}

func TestUpdateDialogButtonsCanInstallOrDismiss(t *testing.T) {
	newDialog := func() *Model {
		return &Model{
			appVersion: "v1.0.1", updateManifestURL: "https://updates.example/manifest.json",
			updateDialog: &updateDialog{currentVersion: "v1.0.1", latestVersion: "v1.1.0"},
		}
	}

	dismissed := newDialog()
	_, _ = dismissed.handleUpdateDialogKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyRight}))
	if dismissed.updateDialog.selected != 1 {
		t.Fatal("right key did not select Later")
	}
	_, command := dismissed.handleUpdateDialogKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if dismissed.updateDialog != nil || command != nil {
		t.Fatal("Later did not close the update dialog")
	}

	installing := newDialog()
	_, command = installing.handleUpdateDialogKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if command == nil || installing.updateDialog.phase != updateInstalling {
		t.Fatal("Update did not start installation")
	}
}

func TestFailedUpdateCanBeDismissed(t *testing.T) {
	m := &Model{updateDialog: &updateDialog{phase: updateFailed, selected: 1}}
	_, _ = m.handleUpdateDialogKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if m.updateDialog != nil {
		t.Fatal("Later did not dismiss a failed update")
	}
}
