package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSSHSessionsFromHistoryCommand(t *testing.T) {
	sessions := sshSessionsFromCommand(`cd /tmp && ssh -p 2202 -i "~/.ssh/prod key" admin@example.com`)
	if len(sessions) != 1 {
		t.Fatalf("sessions = %#v", sessions)
	}
	got := sessions[0]
	if got.Host != "example.com" || got.User != "admin" || got.Port != 2202 || got.KeyPath != "~/.ssh/prod key" {
		t.Fatalf("parsed session = %#v", got)
	}

	urlSessions := sshSessionsFromCommand("ssh ssh://deploy@[2001:db8::1]:2222")
	if len(urlSessions) != 1 || urlSessions[0].Host != "2001:db8::1" || urlSessions[0].User != "deploy" || urlSessions[0].Port != 2222 {
		t.Fatalf("parsed URL session = %#v", urlSessions)
	}
}

func TestHistoryCommandsRecognizeZshAndFishFormats(t *testing.T) {
	got := historyCommands(": 1700000000:0;ssh root@zsh.example\n- cmd: ssh fish.example\n")
	want := []string{"ssh root@zsh.example", "ssh fish.example"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %#v, want %#v", got, want)
	}
}

func TestScanSSHHistoryReadsAndDeduplicatesRecentConnections(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HISTFILE", filepath.Join(home, ".zsh_history"))
	data := []byte("ssh old@one.example\nssh -p 2222 root@two.example\nssh new@one.example\nssh new@one.example\n")
	if err := os.WriteFile(filepath.Join(home, ".zsh_history"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	sessions, err := scanSSHHistory()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 3 {
		t.Fatalf("sessions = %#v", sessions)
	}
	if sessions[0].User != "new" || sessions[0].Host != "one.example" || sessions[1].Host != "two.example" {
		t.Fatalf("recent order = %#v", sessions)
	}
}

func TestSSHPortOptionAnywhereInTheCommand(t *testing.T) {
	cases := []struct {
		command string
		user    string
		host    string
		port    int
		key     string
	}{
		{"ssh -p 2222 root@19.12.12.22", "root", "19.12.12.22", 2222, ""},
		{"ssh root@19.12.12.22 -p 2222", "root", "19.12.12.22", 2222, ""},
		{"ssh root@19.12.12.22 -p2222", "root", "19.12.12.22", 2222, ""},
		{"ssh -l admin -p 2222 19.12.12.22", "admin", "19.12.12.22", 2222, ""},
		{"ssh 19.12.12.22 -l admin", "admin", "19.12.12.22", 22, ""},
		{"ssh root@19.12.12.22 -o Port=2200", "root", "19.12.12.22", 2200, ""},
		{"ssh -oPort=2200 -oUser=deploy 19.12.12.22", "deploy", "19.12.12.22", 2200, ""},
		{`ssh -o "Port 2200" root@19.12.12.22`, "root", "19.12.12.22", 2200, ""},
		{"ssh root@19.12.12.22 -i ~/.ssh/router -p 2222", "root", "19.12.12.22", 2222, "~/.ssh/router"},
		{"ssh -p 2222 root@19.12.12.22:2200", "root", "19.12.12.22", 2222, ""}, // -p wins
		// Words after the first plain word belong to the remote command.
		{"ssh root@19.12.12.22 ls -p 7", "root", "19.12.12.22", 22, ""},
	}
	for _, tc := range cases {
		sessions := sshSessionsFromCommand(tc.command)
		if len(sessions) != 1 {
			t.Fatalf("%q: %d sessions", tc.command, len(sessions))
		}
		got := sessions[0]
		if got.User != tc.user || got.Host != tc.host || got.Port != tc.port || got.KeyPath != tc.key {
			t.Errorf("%q: got %s@%s:%d key=%q", tc.command, got.User, got.Host, got.Port, got.KeyPath)
		}
	}
	for _, broken := range []string{"ssh root@19.12.12.22 -p", "ssh root@19.12.12.22 -p abc", "ssh -p 70000 root@host"} {
		if sessions := sshSessionsFromCommand(broken); len(sessions) != 0 {
			t.Errorf("%q was accepted as %+v", broken, sessions)
		}
	}
}
