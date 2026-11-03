package tui

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"ssh-manager-tui/internal/config"
)

func typeText(m *Model, text string) {
	for _, r := range text {
		m.handleSessionKey(tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)}))
	}
}

func TestFuzzyScorePrefersPrefixesAndWordStarts(t *testing.T) {
	if fuzzyScore("prod", "home-nas") != 0 {
		t.Fatal("unrelated text matched")
	}
	prefix, word, middle := fuzzyScore("web", "web-1"), fuzzyScore("web", "prod-web-1"), fuzzyScore("web", "cobweb")
	if !(prefix > word && word > middle && middle > 0) {
		t.Fatalf("scores prefix=%d word=%d middle=%d", prefix, word, middle)
	}
	if fuzzyScore("pw1", "prod-web-1") == 0 {
		t.Fatal("subsequence did not match")
	}
}

func TestParseQuickTarget(t *testing.T) {
	cases := []struct {
		query string
		ok    bool
		want  config.Session
	}{
		{"deploy@example.com", true, config.Session{Host: "example.com", User: "deploy", Port: 22}},
		{"deploy@example.com:2222", true, config.Session{Host: "example.com", User: "deploy", Port: 2222}},
		{"ssh -p 2200 root@10.0.0.5", true, config.Session{Host: "10.0.0.5", User: "root", Port: 2200}},
		{"prod", false, config.Session{}},
		{"deploy@", false, config.Session{}},
		{"example.", false, config.Session{}},
		{"two words.com", false, config.Session{}},
	}
	for _, tc := range cases {
		got, ok := parseQuickTarget(tc.query)
		if ok != tc.ok || (ok && (got.Host != tc.want.Host || got.User != tc.want.User || got.Port != tc.want.Port)) {
			t.Errorf("parseQuickTarget(%q) = %+v, %v", tc.query, got, ok)
		}
	}
}

func TestLauncherShowsRecentGroupsAndUnsavedHistory(t *testing.T) {
	now := time.Now()
	m := &Model{width: 100, height: 30, page: pageSessions}
	for i, name := range []string{"a", "b", "c", "d", "e", "f"} {
		m.config.Sessions = append(m.config.Sessions, config.Session{
			Name: name, Host: name + ".example", Port: 22, User: "root", Group: "G",
			LastConnected: now.Add(-time.Duration(i) * time.Hour),
		})
	}
	m.historyHosts = []historyHost{
		{session: config.Session{Name: "a.example", Host: "a.example", Port: 22, User: "root"}, uses: 9},
		{session: config.Session{Name: "new.example", Host: "new.example", Port: 22, User: "root"}, uses: 2},
	}
	rows := m.launchRows()
	if rows[0].section != "Недавние" || rows[0].session.Name != "a" || rows[2].session.Name != "c" {
		t.Fatalf("recent rows = %+v", rows[:3])
	}
	last := rows[len(rows)-1]
	if last.kind != launchHistory || last.session.Host != "new.example" {
		t.Fatalf("history row = %+v", last)
	}
	for _, row := range rows {
		if row.kind == launchHistory && row.session.Host == "a.example" {
			t.Fatal("a history host that is already saved is listed twice")
		}
	}
	if len(rows) != 6+1 {
		t.Fatalf("rows = %d, recent servers must not be repeated in their group", len(rows))
	}
}

func TestQuickConnectRowAppearsOnlyForUnknownAddresses(t *testing.T) {
	m := &Model{width: 100, height: 30, page: pageSessions, config: config.Config{Sessions: []config.Session{
		{Name: "prod", Host: "prod.example", Port: 22, User: "deploy"},
	}}}
	typeText(m, "deploy@prod.example")
	for _, row := range m.launchRows() {
		if row.kind == launchQuick {
			t.Fatal("quick-connect row offered for a saved server")
		}
	}
	m.handleSessionKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	typeText(m, "deploy@new.example:2200")
	rows := m.launchRows()
	if len(rows) == 0 || rows[0].kind != launchQuick || rows[0].session.Port != 2200 {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestNewServerIsSavedOnlyAfterSuccessfulConnection(t *testing.T) {
	store := config.NewStoreAt(filepath.Join(t.TempDir(), "config.json"))
	m := &Model{store: store, width: 100, height: 30, page: pageSessions, tabID: 1}
	typeText(m, "deploy@new.example")
	_, cmd := m.handleSessionKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd == nil || !m.connecting || m.pendingSave == nil {
		t.Fatalf("enter: cmd=%v connecting=%v pending=%v", cmd != nil, m.connecting, m.pendingSave)
	}
	if len(m.config.Sessions) != 0 {
		t.Fatal("server saved before connecting")
	}

	profile := *m.pendingSave
	profile.Password = "secret"
	saved, added, err := m.savePendingHost(profile)
	if err != nil || !added || saved.Name != "new.example" {
		t.Fatalf("savePendingHost = %+v, %v, %v", saved, added, err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Sessions) != 1 || loaded.Sessions[0].Host != "new.example" || loaded.Sessions[0].Password != "secret" {
		t.Fatalf("saved sessions = %+v", loaded.Sessions)
	}
	if _, added, _ := m.savePendingHost(profile); added {
		t.Fatal("pending host saved twice")
	}
}

func TestDeleteAsksForConfirmation(t *testing.T) {
	store := config.NewStoreAt(filepath.Join(t.TempDir(), "config.json"))
	m := &Model{store: store, width: 100, height: 30, page: pageSessions, config: config.Config{Sessions: []config.Session{
		{Name: "keep", Host: "keep.example", Port: 22, User: "root"},
		{Name: "drop", Host: "drop.example", Port: 22, User: "root"},
	}}}
	typeText(m, "drop")
	// With a query Delete edits the search field instead of deleting.
	m.handleSessionKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyDelete}))
	if m.confirm {
		t.Fatal("Delete with a query must not ask to delete a server")
	}
	m.handleSessionKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	// Alphabetical order without groups: "drop" is highlighted first.
	m.handleSessionKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyDelete}))
	if !m.confirm {
		t.Fatal("Delete did not ask for confirmation")
	}
	m.handleSessionKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if len(m.config.Sessions) != 1 || m.config.Sessions[0].Name != "keep" {
		t.Fatalf("sessions after delete = %+v", m.config.Sessions)
	}
}

func TestRussianPluralsAndRelativeTime(t *testing.T) {
	for n, want := range map[int]string{1: "сервер", 2: "сервера", 5: "серверов", 11: "серверов", 21: "сервер", 104: "сервера"} {
		if got := ruPlural(n, "сервер", "сервера", "серверов"); got != want {
			t.Errorf("ruPlural(%d) = %q, want %q", n, got, want)
		}
	}
	now := time.Date(2026, 11, 3, 15, 0, 0, 0, time.UTC)
	for moment, want := range map[time.Time]string{
		now.Add(-20 * time.Second):                    "только что",
		now.Add(-5 * time.Minute):                     "5 минут назад",
		now.Add(-3 * time.Hour):                       "3 часа назад",
		time.Date(2026, 11, 2, 23, 0, 0, 0, time.UTC): "вчера",
		time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC): "4 дня назад",
		time.Date(2026, 3, 8, 9, 0, 0, 0, time.UTC):   "8 мар",
		time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC):   "1 мая 2025",
	} {
		if got := relativeTime(moment, now); got != want {
			t.Errorf("relativeTime(%v) = %q, want %q", moment, got, want)
		}
	}
}

func TestHalfTypedAddressDoesNotBeatMatchingServer(t *testing.T) {
	m := &Model{width: 100, height: 30, page: pageSessions}
	m.historyHosts = []historyHost{{session: config.Session{Name: "127.0.0.1", Host: "127.0.0.1", Port: 2222, User: "root"}, uses: 2}}
	typeText(m, "127.0")
	rows := m.launchRows()
	if len(rows) != 2 || rows[0].kind != launchHistory || rows[1].kind != launchQuick {
		t.Fatalf("rows = %+v", rows)
	}
	m.handleSessionKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	typeText(m, "root@127.0")
	if rows := m.launchRows(); rows[0].kind != launchQuick {
		t.Fatalf("explicit user@host should come first: %+v", rows)
	}
}

func TestFriendlyConnectError(t *testing.T) {
	profile := config.Session{Host: "db.example", Port: 2222}
	got := friendlyConnectError(errors.New("dial db.example:2222: dial tcp: lookup db.example: no such host"), profile)
	if got != "сервер «db.example» не найден — проверьте адрес" {
		t.Fatalf("friendly error = %q", got)
	}
	if got := friendlyConnectError(errors.New("dial tcp 10.0.0.1:2222: connect: connection refused"), profile); got != "db.example не принимает подключения на порту 2222" {
		t.Fatalf("friendly error = %q", got)
	}
}
