package remote

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
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
