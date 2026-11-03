package update

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestHelperProcessSleep is not a real test: it keeps a copy of the test
// binary running so the update can replace a live executable.
func TestHelperProcessSleep(t *testing.T) {
	if os.Getenv("SSHM_UPDATE_HELPER") != "1" {
		t.Skip("helper process")
	}
	time.Sleep(30 * time.Second)
}

// TestInstallReplacesRunningExecutable is the case that failed on Windows:
// the executable being updated is the one currently running. CI runs it on
// windows-latest.
func TestInstallReplacesRunningExecutable(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	name := "sshm"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	target := filepath.Join(directory, name)
	copyFile(t, self, target)

	running := exec.Command(target, "-test.run=^TestHelperProcessSleep$")
	running.Env = append(os.Environ(), "SSHM_UPDATE_HELPER=1")
	if err := running.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = running.Process.Kill()
		_ = running.Wait()
	}()
	time.Sleep(300 * time.Millisecond)

	source := filepath.Join(directory, ".sshm-update-test")
	if err := os.WriteFile(source, []byte("new version"), 0o755); err != nil {
		t.Fatal(err)
	}
	deferred, err := installExecutable(source, target)
	if err != nil || deferred {
		t.Fatalf("installExecutable while running: deferred=%v err=%v", deferred, err)
	}
	if got, _ := os.ReadFile(target); string(got) != "new version" {
		t.Fatalf("target was not replaced: %d bytes", len(got))
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	in, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}
