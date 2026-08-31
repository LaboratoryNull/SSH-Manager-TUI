package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadLocalDirectorySortsDirectoriesFirst(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a-file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "z-dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	msg := loadLocalDirectory(dir, nil)().(localDirLoadedMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if len(msg.entries) != 2 || !msg.entries[0].dir || msg.entries[0].name != "z-dir" {
		t.Fatalf("entries = %#v", msg.entries)
	}
}

func TestFormatBytes(t *testing.T) {
	if got := formatBytes(1536); got != "1.5 KiB" {
		t.Fatalf("formatBytes = %q", got)
	}
}
