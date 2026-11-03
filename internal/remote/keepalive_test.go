package remote

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"ssh-manager-tui/internal/config"
)

// testServer is a minimal SSH server with a PTY shell and SFTP. With
// ignoreKeepalive it never answers global requests, like some embedded
// SSH servers (MikroTik RouterOS was reported to behave this way).
func testServer(t *testing.T, ignoreKeepalive *atomic.Bool) (host string, port int, keyPath string) {
	t.Helper()
	_, hostPrivate, _ := ed25519.GenerateKey(rand.Reader)
	hostSigner, err := ssh.NewSignerFromKey(hostPrivate)
	if err != nil {
		t.Fatal(err)
	}
	clientPublic, clientPrivate, _ := ed25519.GenerateKey(rand.Reader)
	authorized, _ := ssh.NewPublicKey(clientPublic)
	pemBlock, err := ssh.MarshalPrivateKey(clientPrivate, "")
	if err != nil {
		t.Fatal(err)
	}
	keyPath = filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(pemBlock), 0o600); err != nil {
		t.Fatal(err)
	}
	serverConfig := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if string(key.Marshal()) == string(authorized.Marshal()) {
			return nil, nil
		}
		return nil, errors.New("denied")
	}}
	serverConfig.AddHostKey(hostSigner)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			raw, err := listener.Accept()
			if err != nil {
				return
			}
			go serveTestConn(raw, serverConfig, ignoreKeepalive)
		}
	}()
	addr := listener.Addr().(*net.TCPAddr)
	return "127.0.0.1", addr.Port, keyPath
}

func serveTestConn(raw net.Conn, serverConfig *ssh.ServerConfig, ignoreKeepalive *atomic.Bool) {
	_, chans, reqs, err := ssh.NewServerConn(raw, serverConfig)
	if err != nil {
		return
	}
	go func() {
		for req := range reqs {
			if req.WantReply && !ignoreKeepalive.Load() {
				_ = req.Reply(false, nil)
			}
		}
	}()
	for newChannel := range chans {
		channel, requests, err := newChannel.Accept()
		if err != nil {
			continue
		}
		go func() {
			for req := range requests {
				switch req.Type {
				case "subsystem":
					_ = req.Reply(true, nil)
					server, _ := sftp.NewServer(channel)
					go func() { _ = server.Serve(); channel.Close() }()
				case "shell":
					_ = req.Reply(true, nil)
					go func() { _, _ = io.Copy(channel, channel) }()
				default:
					if req.WantReply {
						_ = req.Reply(true, nil)
					}
				}
			}
		}()
	}
}

func connectTest(t *testing.T, host string, port int, keyPath string) *Connection {
	t.Helper()
	profile := config.Session{Name: "test", Host: host, Port: port, User: "test", KeyPath: keyPath}
	conn, err := Connect(context.Background(), profile, 80, 24, true)
	var unknown *HostKeyMismatchError
	if errors.As(err, &unknown) {
		if err := AcceptHostKey(unknown); err != nil {
			t.Fatal(err)
		}
		conn, err = Connect(context.Background(), profile, 80, 24, true)
	}
	if err != nil {
		t.Fatalf("connect %s:%s: %v", host, strconv.Itoa(port), err)
	}
	t.Cleanup(conn.Close)
	return conn
}

func shortenKeepalive(t *testing.T) {
	interval, timeout := keepaliveInterval, keepaliveTimeout
	keepaliveInterval, keepaliveTimeout = 100*time.Millisecond, 150*time.Millisecond
	t.Cleanup(func() { keepaliveInterval, keepaliveTimeout = interval, timeout })
}

func TestSessionSurvivesServerThatIgnoresKeepalive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	shortenKeepalive(t)
	ignore := new(atomic.Bool)
	ignore.Store(true)
	host, port, key := testServer(t, ignore)
	conn := connectTest(t, host, port, key)
	time.Sleep(1200 * time.Millisecond)
	if err := conn.TerminalError(); err != nil {
		t.Fatalf("idle session to a server without keepalive replies was dropped: %v", err)
	}
	// The shell still works.
	if err := conn.Send([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	select {
	case data := <-conn.Output:
		if string(data) != "ping" {
			t.Fatalf("echo = %q", data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no echo from shell")
	}
}

func TestKeepaliveDetectsServerThatStopsAnswering(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	shortenKeepalive(t)
	ignore := new(atomic.Bool)
	host, port, key := testServer(t, ignore)
	conn := connectTest(t, host, port, key)
	time.Sleep(400 * time.Millisecond)
	if err := conn.TerminalError(); err != nil {
		t.Fatalf("healthy session failed: %v", err)
	}
	// The server answered before; going silent now means the link is dead.
	ignore.Store(true)
	deadline := time.Now().Add(2 * time.Second)
	for conn.TerminalError() == nil && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if err := conn.TerminalError(); err == nil {
		t.Fatal("a server that stopped answering keepalives was not detected")
	}
}
