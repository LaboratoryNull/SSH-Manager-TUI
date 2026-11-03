package remote

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestHostKeyCallbackRequiresConfirmationForUnknownAndChangedKeys(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	publicKey := func() ssh.PublicKey {
		public, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		key, err := ssh.NewPublicKey(public)
		if err != nil {
			t.Fatal(err)
		}
		return key
	}
	address := &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 22}
	first := publicKey()

	callback, err := hostKeyCallback()
	if err != nil {
		t.Fatal(err)
	}
	err = callback("example.test:22", address, first)
	var unknown *HostKeyMismatchError
	if !errors.As(err, &unknown) || len(unknown.Expected) != 0 {
		t.Fatalf("unknown key error = %#v", err)
	}
	if err := AcceptHostKey(unknown); err != nil {
		t.Fatal(err)
	}

	callback, err = hostKeyCallback()
	if err != nil {
		t.Fatal(err)
	}
	if err := callback("example.test:22", address, first); err != nil {
		t.Fatalf("accepted key was rejected: %v", err)
	}
	err = callback("example.test:22", address, publicKey())
	var changed *HostKeyMismatchError
	if !errors.As(err, &changed) || len(changed.Expected) != 1 {
		t.Fatalf("changed key error = %#v", err)
	}
}

func TestConnectionKeepsFirstTerminalFailure(t *testing.T) {
	connection := &Connection{}
	first := errors.New("keepalive timeout")
	connection.setTerminalError(first)
	connection.setTerminalError(errors.New("session closed"))
	if got := connection.TerminalError(); !errors.Is(got, first) {
		t.Fatalf("terminal error = %v, want first failure %v", got, first)
	}
}

func TestConnectionFailureClosesTransport(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	connection := &Connection{netConn: client}
	wantErr := errors.New("connection lost")
	connection.fail(wantErr)
	if got := connection.TerminalError(); !errors.Is(got, wantErr) {
		t.Fatalf("terminal error = %v, want %v", got, wantErr)
	}
	buffer := make([]byte, 1)
	if _, err := server.Read(buffer); err == nil {
		t.Fatal("transport remained open after terminal failure")
	}
}

func TestAuthenticationErrorCanBeIdentifiedThroughWrapping(t *testing.T) {
	cause := errors.New("ssh: unable to authenticate, no supported methods remain")
	err := fmt.Errorf("connect: %w", authenticationError(cause))
	if !IsAuthenticationError(err) {
		t.Fatalf("authentication error was not identified: %v", err)
	}
	if IsAuthenticationError(errors.New("dial timeout")) {
		t.Fatal("network error was identified as an authentication failure")
	}
}

func TestPreferredHostKeyAlgorithmsPutsKnownKeyTypesFirst(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	address := &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 22}
	callback, err := hostKeyCallback()
	if err != nil {
		t.Fatal(err)
	}
	if got := preferredHostKeyAlgorithms(callback, "example.test:22", address); got != nil {
		t.Fatalf("unknown host algorithms = %v, want defaults (nil)", got)
	}

	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	var unknown *HostKeyMismatchError
	if !errors.As(callback("example.test:22", address, key), &unknown) {
		t.Fatal("expected unknown host error")
	}
	if err := AcceptHostKey(unknown); err != nil {
		t.Fatal(err)
	}
	callback, err = hostKeyCallback()
	if err != nil {
		t.Fatal(err)
	}
	got := preferredHostKeyAlgorithms(callback, "example.test:22", address)
	if len(got) == 0 || got[0] != ssh.KeyAlgoED25519 {
		t.Fatalf("algorithms = %v, want %s first", got, ssh.KeyAlgoED25519)
	}
	if len(got) != len(ssh.SupportedAlgorithms().HostKeys) {
		t.Fatalf("algorithms = %v, want every supported algorithm once", got)
	}
}

func TestExpandHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for input, want := range map[string]string{
		"~":           home,
		"~/.ssh/id":   home + "/.ssh/id",
		"/etc/key":    "/etc/key",
		"~other/file": "~other/file",
	} {
		if got := expandHome(input); got != want {
			t.Errorf("expandHome(%q) = %q, want %q", input, got, want)
		}
	}
}
