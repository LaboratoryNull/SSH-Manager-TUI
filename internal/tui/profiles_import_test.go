package tui

import (
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"ssh-manager-tui/internal/config"
)

func TestUserProfileRenameUpdatesLinkedHosts(t *testing.T) {
	store := config.NewStoreAt(filepath.Join(t.TempDir(), "config.json"))
	m := &Model{
		store: store,
		config: config.Config{
			UserProfiles: []config.UserProfile{{Name: "old", User: "root", Password: "secret"}},
			Sessions:     []config.Session{{Name: "prod", Host: "prod.example", Port: 22, CredentialProfile: "old"}},
		},
		profileManager: &profileManager{},
	}
	editor := newProfileEditor(&m.config.UserProfiles[0], 0)
	editor.inputs[0].SetValue("production-root")
	editor.inputs[1].SetValue("deploy")
	m.saveUserProfile(editor)

	if m.config.Sessions[0].CredentialProfile != "production-root" {
		t.Fatalf("host profile reference = %q", m.config.Sessions[0].CredentialProfile)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := loaded.ResolveSession(loaded.Sessions[0])
	if err != nil {
		t.Fatal(err)
	}
	if resolved.User != "deploy" || resolved.Password != "secret" {
		t.Fatalf("resolved session = %#v", resolved)
	}
}

func TestHostFormCanSelectCredentialProfileOrUseManualCredentials(t *testing.T) {
	m := &Model{config: config.Config{UserProfiles: []config.UserProfile{{Name: "root-key", User: "root", KeyPath: "/key"}}}}
	m.resetForm()
	m.cycleFormCredential(1)
	m.inputs[hostNameInput].SetValue("prod")
	m.inputs[hostAddressInput].SetValue("prod.example")
	linked, err := m.formProfile()
	if err != nil {
		t.Fatal(err)
	}
	if linked.CredentialProfile != "root-key" || linked.User != "" || linked.KeyPath != "" {
		t.Fatalf("linked host = %#v", linked)
	}
	resolved, err := m.config.ResolveSession(linked)
	if err != nil || resolved.User != "root" || resolved.KeyPath != "/key" {
		t.Fatalf("resolved linked host = %#v, err = %v", resolved, err)
	}

	m.cycleFormCredential(1)
	m.inputs[hostUserInput].SetValue("manual")
	manual, err := m.formProfile()
	if err != nil {
		t.Fatal(err)
	}
	if manual.CredentialProfile != "" || manual.User != "manual" {
		t.Fatalf("manual host = %#v", manual)
	}
}

func TestFinishHistoryImportAssignsProfileAndGroup(t *testing.T) {
	store := config.NewStoreAt(filepath.Join(t.TempDir(), "config.json"))
	m := &Model{
		store:  store,
		config: config.Config{UserProfiles: []config.UserProfile{{Name: "deploy-key", User: "deploy", KeyPath: "/key"}}},
		importWizard: &importWizard{entries: []importCandidate{{
			session:  config.Session{Name: "server.example", Host: "server.example", Port: 22, User: "local", KeyPath: "/history-key"},
			selected: true,
			profile:  "deploy-key",
			group:    "Production",
		}}},
	}
	m.finishHistoryImport()

	if m.importWizard != nil || len(m.config.Sessions) != 1 {
		t.Fatalf("import state: wizard=%#v sessions=%#v", m.importWizard, m.config.Sessions)
	}
	got := m.config.Sessions[0]
	if got.Group != "Production" || got.CredentialProfile != "deploy-key" || got.User != "" || got.KeyPath != "" {
		t.Fatalf("imported host = %#v", got)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := loaded.ResolveSession(loaded.Sessions[0])
	if err != nil || resolved.User != "deploy" || resolved.KeyPath != "/key" {
		t.Fatalf("resolved imported host = %#v, err = %v", resolved, err)
	}
}

func TestImportGroupEditorCompletesExistingGroup(t *testing.T) {
	m := &Model{
		config: config.Config{Sessions: []config.Session{{Name: "prod", Host: "old.example", Port: 22, User: "root", Group: "Production"}}},
		importWizard: &importWizard{entries: []importCandidate{{session: config.Session{
			Name: "new.example", Host: "new.example", Port: 22, User: "root",
		}}}},
	}
	_, _ = m.handleImportKey(tea.KeyPressMsg(tea.Key{Code: 'g', Text: "g"}))
	m.importWizard.groupInput.SetValue("pro")
	_, _ = m.handleImportKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	if got := m.importWizard.groupInput.Value(); got != "Production" {
		t.Fatalf("completed group = %q", got)
	}
	_, _ = m.handleImportKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if got := m.importWizard.entries[0].group; got != "Production" {
		t.Fatalf("assigned group = %q", got)
	}
}

func TestImportDoneControlIsClickable(t *testing.T) {
	store := config.NewStoreAt(filepath.Join(t.TempDir(), "config.json"))
	m := &Model{
		store: store, width: 100, height: 30, page: pageSessions, tabID: 1,
		importWizard: &importWizard{entries: []importCandidate{{
			session: config.Session{Name: "server", Host: "server.example", Port: 22, User: "root"}, selected: true,
		}}},
	}
	m.resetForm()
	m.tabs = []*tabState{m.captureTab()}
	foundX, foundY := -1, -1
	for y := 1; y < m.height && foundX < 0; y++ {
		for x := 0; x < m.width; x++ {
			if m.clickedHint(x, y) == "Завершено" {
				foundX, foundY = x, y
				break
			}
		}
	}
	if foundX < 0 {
		t.Fatal("done control has no mouse hit target")
	}
	_, _ = m.handleMouseClick(tea.MouseClickMsg(tea.Mouse{X: foundX, Y: foundY, Button: tea.MouseLeft}))
	if m.importWizard != nil || len(m.config.Sessions) != 1 {
		t.Fatalf("click did not finish import: wizard=%#v sessions=%#v", m.importWizard, m.config.Sessions)
	}
}
