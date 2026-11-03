package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRunDownloadsVerifiesAndAtomicallyReplacesExecutable(t *testing.T) {
	oldBinary := []byte("old binary")
	newBinary := []byte("new verified binary")
	digest := sha256.Sum256(newBinary)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("PRIVATE-TOKEN") != "secret" {
			http.Error(response, "missing token", http.StatusUnauthorized)
			return
		}
		switch request.URL.Path {
		case "/manifest.json":
			_ = json.NewEncoder(response).Encode(Manifest{
				Version: "v1.1.0",
				Artifacts: map[string]Artifact{"linux-amd64": {
					URL: server.URL + "/sshm-linux-amd64", SHA256: hex.EncodeToString(digest[:]), Size: int64(len(newBinary)),
				}},
			})
		case "/sshm-linux-amd64":
			_, _ = response.Write(newBinary)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	target := filepath.Join(t.TempDir(), "sshm")
	if err := os.WriteFile(target, oldBinary, 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := Run(context.Background(), Options{
		CurrentVersion: "v1.0.0", ManifestURL: server.URL + "/manifest.json",
		Token: "secret", Executable: target, GOOS: "linux", GOARCH: "amd64",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Available || !result.Updated || result.LatestVersion != "v1.1.0" {
		t.Fatalf("result = %#v", result)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(newBinary) {
		t.Fatalf("installed binary = %q", got)
	}
	// Windows has no executable bit; Unix must keep the binary runnable.
	if info, err := os.Stat(target); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0) {
		t.Fatalf("updated executable mode = %v, err = %v", info.Mode(), err)
	}
}

func TestRunCheckOnlyDoesNotReplaceExecutable(t *testing.T) {
	server := manifestServer(t, "v2.0.0", []byte("new binary"), false)
	defer server.Close()
	target := filepath.Join(t.TempDir(), "sshm")
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := Run(context.Background(), Options{
		CurrentVersion: "v1.0.0", ManifestURL: server.URL + "/manifest.json",
		Executable: target, CheckOnly: true, GOOS: "linux", GOARCH: "amd64",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Available || result.Updated {
		t.Fatalf("result = %#v", result)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "old binary" {
		t.Fatalf("check-only replaced executable with %q", got)
	}
}

func TestRunRejectsChecksumMismatchWithoutTouchingExecutable(t *testing.T) {
	server := manifestServer(t, "v1.1.0", []byte("new binary"), true)
	defer server.Close()
	target := filepath.Join(t.TempDir(), "sshm")
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Run(context.Background(), Options{
		CurrentVersion: "v1.0.0", ManifestURL: server.URL + "/manifest.json",
		Executable: target, GOOS: "linux", GOARCH: "amd64",
	})
	if err == nil {
		t.Fatal("checksum mismatch was accepted")
	}
	got, _ := os.ReadFile(target)
	if string(got) != "old binary" {
		t.Fatalf("failed update changed executable to %q", got)
	}
}

func TestCompareReleaseVersions(t *testing.T) {
	tests := []struct {
		current, latest string
		want            int
	}{
		{"v1.0.0", "v1.0.1", -1},
		{"v1.2.0", "v1.1.9", 1},
		{"v1.0.0", "v1.0.0", 0},
		{"v1.0.0-beta.2", "v1.0.0", -1},
		{"dev-abcdef", "v1.0.0", -1},
	}
	for _, test := range tests {
		got, err := compareReleaseVersions(test.current, test.latest)
		if err != nil {
			t.Fatalf("compare %q and %q: %v", test.current, test.latest, err)
		}
		if got != test.want {
			t.Fatalf("compare %q and %q = %d, want %d", test.current, test.latest, got, test.want)
		}
	}
}

func TestSecureURLRejectsPlainRemoteHTTP(t *testing.T) {
	if _, err := secureURL("http://updates.example/manifest.json"); err == nil {
		t.Fatal("plain remote HTTP URL was accepted")
	}
}

func manifestServer(t *testing.T, version string, binary []byte, corruptHash bool) *httptest.Server {
	t.Helper()
	digest := sha256.Sum256(binary)
	hash := hex.EncodeToString(digest[:])
	if corruptHash {
		hash = "0000000000000000000000000000000000000000000000000000000000000000"
	}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/manifest.json":
			_ = json.NewEncoder(response).Encode(Manifest{
				Version: version,
				Artifacts: map[string]Artifact{"linux-amd64": {
					URL: server.URL + "/sshm-linux-amd64", SHA256: hash, Size: int64(len(binary)),
				}},
			})
		case "/sshm-linux-amd64":
			_, _ = response.Write(binary)
		default:
			http.NotFound(response, request)
		}
	}))
	return server
}

func TestReplaceByRenameSwapsAndRollsBack(t *testing.T) {
	direct := func(operation func() error) error { return operation() }
	directory := t.TempDir()
	target := filepath.Join(directory, "sshm.exe")
	source := filepath.Join(directory, ".sshm-update-1")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target+previousSuffix, []byte("stale"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceByRename(source, target, direct); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(target); string(got) != "new" {
		t.Fatalf("target = %q", got)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatal("downloaded file was not moved into place")
	}

	// A missing download must leave the current executable in place.
	if err := replaceByRename(filepath.Join(directory, "missing"), target, direct); err == nil {
		t.Fatal("expected an error for a missing download")
	}
	if got, _ := os.ReadFile(target); string(got) != "new" {
		t.Fatalf("target after rollback = %q", got)
	}
}

func TestReplaceByRenameWorksWhenOldLeftoverIsBusy(t *testing.T) {
	direct := func(operation func() error) error { return operation() }
	directory := t.TempDir()
	target := filepath.Join(directory, "sshm.exe")
	source := filepath.Join(directory, ".sshm-update-2")
	_ = os.WriteFile(target, []byte("old"), 0o755)
	_ = os.WriteFile(source, []byte("new"), 0o755)
	// A non-empty directory cannot be removed, standing in for a .old file
	// that is still locked by an open window of an earlier version.
	busy := target + previousSuffix
	if err := os.MkdirAll(filepath.Join(busy, "locked"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceByRename(source, target, direct); err != nil {
		t.Fatalf("replace with a busy leftover: %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "new" {
		t.Fatalf("target = %q", got)
	}
}
