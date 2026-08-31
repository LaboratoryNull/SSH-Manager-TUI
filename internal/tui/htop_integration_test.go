package tui

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// Set SSH_MANAGER_HTOP_CAPTURE to a typescript produced by `script` to run
// this compatibility check against a real ncurses/htop output stream.
func TestHtopCapture(t *testing.T) {
	capturePath := os.Getenv("SSH_MANAGER_HTOP_CAPTURE")
	if capturePath == "" {
		t.Skip("SSH_MANAGER_HTOP_CAPTURE is not set")
	}
	data, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	start := bytes.Index(data, []byte("\x1b[?1049h"))
	end := bytes.LastIndex(data, []byte("\x1b[?1049l"))
	if start < 0 {
		t.Fatal("capture does not contain an alternate-screen htop session")
	}
	if end <= start {
		end = len(data)
	}
	screen := newTerminalScreen(80, 24)
	screen.Feed(data[start:end])
	if !screen.altActive {
		t.Fatal("htop did not activate the alternate screen")
	}
	plain := screen.String()
	if !strings.Contains(plain, "Tasks:") || !strings.Contains(plain, "F10") {
		t.Fatalf("important htop content was not rendered:\n%s", plain)
	}
	styled := 0
	for _, row := range screen.rows {
		for _, cell := range row {
			if cell.style != (terminalStyle{}) {
				styled++
			}
		}
	}
	if styled == 0 {
		t.Fatal("htop colors and attributes were lost")
	}
}
