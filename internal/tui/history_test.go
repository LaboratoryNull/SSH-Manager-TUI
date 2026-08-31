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
