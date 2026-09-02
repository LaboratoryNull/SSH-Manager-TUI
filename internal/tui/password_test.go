package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"ssh-manager-tui/internal/config"
	"ssh-manager-tui/internal/remote"
)

func authenticationFailure() error {
	return fmt.Errorf("SSH handshake: %w", remote.ErrAuthentication)
}

func TestAuthenticationFailureRequestsPasswordThreeTimes(t *testing.T) {
	profile := config.Session{Name: "prod", Host: "prod.example", Port: 22, User: "root", Password: "old"}
	m := &Model{tabID: 1, width: 80, height: 24, page: pageSessions, connecting: true}

	_, _ = m.Update(connectMsg{tabID: 1, profile: profile, err: authenticationFailure()})
	if m.passwordPrompt == nil || m.passwordPrompt.failures != 0 {
		t.Fatalf("password prompt after initial failure = %#v", m.passwordPrompt)
	}
	if got := m.passwordPrompt.input.Value(); got != "" {
		t.Fatalf("prompt contains the old password: %q", got)
	}

	for attempt := 1; attempt <= passwordAttemptLimit; attempt++ {
		m.passwordPrompt.input.SetValue("wrong")
		_, command := m.handlePasswordPrompt(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
		if command == nil || !m.connecting {
			t.Fatalf("attempt %d was not started", attempt)
		}
		_, _ = m.Update(connectMsg{tabID: 1, profile: profile, err: authenticationFailure()})
		if attempt < passwordAttemptLimit {
			if m.passwordPrompt == nil || m.passwordPrompt.failures != attempt {
				t.Fatalf("prompt after attempt %d = %#v", attempt, m.passwordPrompt)
			}
		}
	}

	if m.passwordPrompt != nil {
		t.Fatalf("password prompt remained after %d failures", passwordAttemptLimit)
	}
	if !strings.Contains(m.status, "пароль не подошёл") {
		t.Fatalf("final notification = %q", m.status)
	}
}

func TestSaveConnectedPasswordUpdatesSession(t *testing.T) {
	store := config.NewStoreAt(filepath.Join(t.TempDir(), "config.json"))
	cfg := config.Config{Sessions: []config.Session{{
		Name: "prod", Host: "prod.example", Port: 22, User: "root", Password: "old",
	}}}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	m := &Model{store: store, config: cfg}
	profile := cfg.Sessions[0]
	profile.Password = "new-secret"

	if err := m.saveConnectedPassword(profile); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Sessions[0].Password; got != "new-secret" {
		t.Fatalf("saved session password = %q", got)
	}
}

func TestSaveConnectedPasswordUpdatesCredentialProfile(t *testing.T) {
	store := config.NewStoreAt(filepath.Join(t.TempDir(), "config.json"))
	cfg := config.Config{
		Sessions: []config.Session{{
			Name: "prod", Host: "prod.example", Port: 22, CredentialProfile: "admins",
		}},
		UserProfiles: []config.UserProfile{{Name: "Admins", User: "root", Password: "old"}},
	}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	m := &Model{store: store, config: cfg}
	profile, err := cfg.ResolveSession(cfg.Sessions[0])
	if err != nil {
		t.Fatal(err)
	}
	profile.Password = "new-secret"

	if err := m.saveConnectedPassword(profile); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.UserProfiles[0].Password; got != "new-secret" {
		t.Fatalf("saved credential profile password = %q", got)
	}
	if got := loaded.Sessions[0].Password; got != "" {
		t.Fatalf("resolved password leaked into session: %q", got)
	}
}

func TestSuccessfulPasswordRetryConnectsAndPersistsPassword(t *testing.T) {
	store := config.NewStoreAt(filepath.Join(t.TempDir(), "config.json"))
	profile := config.Session{Name: "prod", Host: "prod.example", Port: 22, User: "root", Password: "old"}
	cfg := config.Config{Sessions: []config.Session{profile}}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	m := &Model{
		store: store, config: cfg, tabID: 1, width: 80, height: 24, page: pageSessions,
		passwordPrompt: newPasswordPrompt(profile), connecting: true,
	}
	profile.Password = "new-secret"
	connection := &remote.Connection{Home: "/home/root", Output: make(chan []byte)}

	_, _ = m.Update(connectMsg{tabID: 1, profile: profile, conn: connection})
	t.Cleanup(connection.Close)
	if m.page != pageConnected || m.conn != connection || m.passwordPrompt != nil {
		t.Fatalf("connection result: page=%v conn=%p prompt=%#v", m.page, m.conn, m.passwordPrompt)
	}
	if m.status != "Подключено, новый пароль сохранён" {
		t.Fatalf("connection status = %q", m.status)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Sessions[0].Password; got != "new-secret" {
		t.Fatalf("saved password = %q", got)
	}
	if got := loaded.Sessions[0].ConnectionCount; got != 1 {
		t.Fatalf("connection count = %d", got)
	}
}
