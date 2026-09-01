package remote

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"ssh-manager-tui/internal/config"
)

type Connection struct {
	SSH     *ssh.Client
	SFTP    *sftp.Client
	Session *ssh.Session
	Stdin   io.WriteCloser
	Output  <-chan []byte
	Home    string

	closeOnce  sync.Once
	netConn    net.Conn
	input      chan []byte
	done       chan struct{}
	failureMu  sync.Mutex
	failure    error
	forwardsMu sync.Mutex
	forwards   map[*Forward]struct{}
}

func (c *Connection) Close() {
	c.closeOnce.Do(func() {
		if c.done != nil {
			close(c.done)
		}
		c.forwardsMu.Lock()
		forwards := make([]*Forward, 0, len(c.forwards))
		for forward := range c.forwards {
			forwards = append(forwards, forward)
		}
		c.forwards = nil
		c.forwardsMu.Unlock()
		for _, forward := range forwards {
			forward.close(false)
		}
		if c.Stdin != nil {
			_ = c.Stdin.Close()
		}
		if c.Session != nil {
			_ = c.Session.Close()
		}
		if c.SFTP != nil {
			_ = c.SFTP.Close()
		}
		if c.SSH != nil {
			_ = c.SSH.Close()
		}
		if c.netConn != nil {
			_ = c.netConn.Close()
		}
	})
}

// TerminalError reports why the interactive shell stopped. A nil value means
// that the remote shell exited normally (for example after the user typed
// "exit"). Transport and keepalive failures are retained so the UI can offer
// to reconnect instead of treating them as an intentional logout.
func (c *Connection) TerminalError() error {
	c.failureMu.Lock()
	defer c.failureMu.Unlock()
	return c.failure
}

func (c *Connection) setTerminalError(err error) {
	if err == nil {
		return
	}
	c.failureMu.Lock()
	if c.failure == nil {
		c.failure = err
	}
	c.failureMu.Unlock()
}

func (c *Connection) fail(err error) {
	c.setTerminalError(err)
	// Closing the underlying socket interrupts a blocked SSH request and all
	// stdout/SFTP reads. Connection.Close is then performed by the UI when it
	// receives the closed output channel.
	if c.netConn != nil {
		_ = c.netConn.Close()
	}
}

func (c *Connection) Send(data []byte) error {
	if c.input == nil || c.done == nil {
		return errors.New("SSH input is closed")
	}
	copyData := append([]byte(nil), data...)
	select {
	case c.input <- copyData:
		return nil
	case <-c.done:
		return errors.New("SSH input is closed")
	}
}

func Connect(ctx context.Context, profile config.Session, width, height int, keyOnly bool) (*Connection, error) {
	auth, err := authMethods(profile, keyOnly)
	if err != nil {
		return nil, err
	}
	hostKeys, err := hostKeyCallback()
	if err != nil {
		return nil, err
	}
	clientConfig := &ssh.ClientConfig{
		User:            profile.User,
		Auth:            auth,
		HostKeyCallback: hostKeys,
		Timeout:         12 * time.Second,
	}
	address := net.JoinHostPort(profile.Host, fmt.Sprintf("%d", profile.Port))
	dialer := net.Dialer{Timeout: 12 * time.Second}
	netConn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", address, err)
	}
	stopContextClose := context.AfterFunc(ctx, func() { _ = netConn.Close() })
	defer stopContextClose()
	handshakeDeadline := time.Now().Add(12 * time.Second)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(handshakeDeadline) {
		handshakeDeadline = deadline
	}
	if err := netConn.SetDeadline(handshakeDeadline); err != nil {
		netConn.Close()
		return nil, fmt.Errorf("set SSH handshake deadline: %w", err)
	}
	conn, chans, reqs, err := ssh.NewClientConn(netConn, address, clientConfig)
	if err != nil {
		netConn.Close()
		return nil, fmt.Errorf("SSH handshake: %w", err)
	}
	sshClient := ssh.NewClient(conn, chans, reqs)
	result := &Connection{SSH: sshClient, netConn: netConn, input: make(chan []byte, 256), done: make(chan struct{})}
	fail := func(err error) (*Connection, error) {
		result.Close()
		return nil, err
	}

	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		return fail(fmt.Errorf("start SFTP: %w", err))
	}
	result.SFTP = sftpClient
	result.Home, err = sftpClient.Getwd()
	if err != nil || result.Home == "" {
		result.Home = "/"
	}

	session, err := sshClient.NewSession()
	if err != nil {
		return fail(fmt.Errorf("create shell: %w", err))
	}
	result.Session = session
	stdin, err := session.StdinPipe()
	if err != nil {
		return fail(fmt.Errorf("open shell input: %w", err))
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		return fail(fmt.Errorf("open shell output: %w", err))
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		return fail(fmt.Errorf("open shell errors: %w", err))
	}
	if width < 1 {
		width = 80
	}
	if height < 1 {
		height = 24
	}
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := session.RequestPty("xterm-256color", height, width, modes); err != nil {
		return fail(fmt.Errorf("request PTY: %w", err))
	}
	// Servers may reject environment variables unless AcceptEnv permits them;
	// TERM itself is already supplied by RequestPty. These optional hints let
	// modern prompts and full-screen programs enable their richest color mode.
	_ = session.Setenv("COLORTERM", "truecolor")
	_ = session.Setenv("TERM_PROGRAM", "ssh-manager-tui")
	if err := session.Shell(); err != nil {
		return fail(fmt.Errorf("start shell: %w", err))
	}
	if err := netConn.SetDeadline(time.Time{}); err != nil {
		return fail(fmt.Errorf("clear SSH setup deadline: %w", err))
	}
	if !stopContextClose() {
		return fail(fmt.Errorf("SSH setup: %w", context.Cause(ctx)))
	}
	result.Stdin = stdin
	go result.writeInput()
	out := make(chan []byte, 128)
	result.Output = out
	go result.monitorOutput(out, stdout, stderr)
	go result.keepalive()
	return result, nil
}

func TestKey(ctx context.Context, profile config.Session) error {
	auth, err := authMethods(profile, true)
	if err != nil {
		return err
	}
	hostKeys, err := hostKeyCallback()
	if err != nil {
		return err
	}
	address := net.JoinHostPort(profile.Host, fmt.Sprintf("%d", profile.Port))
	netConn, err := (&net.Dialer{Timeout: 12 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	defer netConn.Close()
	sshConn, _, _, err := ssh.NewClientConn(netConn, address, &ssh.ClientConfig{
		User: profile.User, Auth: auth, HostKeyCallback: hostKeys, Timeout: 12 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("SSH key authentication: %w", err)
	}
	return sshConn.Close()
}

func (c *Connection) writeInput() {
	for {
		select {
		case data := <-c.input:
			if _, err := c.Stdin.Write(data); err != nil {
				c.fail(fmt.Errorf("write SSH input: %w", err))
				return
			}
		case <-c.done:
			return
		}
	}
}

func (c *Connection) Resize(width, height int) error {
	if c.Session == nil || width < 1 || height < 1 {
		return nil
	}
	return c.Session.WindowChange(height, width)
}

func (c *Connection) monitorOutput(out chan<- []byte, readers ...io.Reader) {
	var wg sync.WaitGroup
	for _, reader := range readers {
		wg.Add(1)
		go func(r io.Reader) {
			defer wg.Done()
			buf := make([]byte, 32*1024)
			for {
				n, err := r.Read(buf)
				if n > 0 {
					chunk := append([]byte(nil), buf[:n]...)
					out <- chunk
				}
				if err != nil {
					if !errors.Is(err, io.EOF) {
						c.setTerminalError(fmt.Errorf("read SSH output: %w", err))
					}
					return
				}
			}
		}(reader)
	}
	waitErr := c.Session.Wait()
	wg.Wait()
	// ssh.ExitError is an exit status or signal produced by the remote command,
	// not a broken transport. ExitMissingError and all other errors mean that
	// the shell disappeared without a proper exit status and should reconnect.
	if waitErr != nil {
		var exitErr *ssh.ExitError
		if !errors.As(waitErr, &exitErr) {
			c.setTerminalError(fmt.Errorf("SSH session: %w", waitErr))
		}
	}
	close(out)
}

const (
	keepaliveInterval = 10 * time.Second
	keepaliveTimeout  = 5 * time.Second
)

func (c *Connection) keepalive() {
	if c.SSH == nil || c.netConn == nil || c.done == nil {
		return
	}
	ticker := time.NewTicker(keepaliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			if err := c.netConn.SetDeadline(time.Now().Add(keepaliveTimeout)); err != nil {
				c.fail(fmt.Errorf("set SSH keepalive deadline: %w", err))
				return
			}
			_, _, err := c.SSH.SendRequest("keepalive@openssh.com", true, nil)
			_ = c.netConn.SetDeadline(time.Time{})
			if err != nil {
				c.fail(fmt.Errorf("SSH keepalive: %w", err))
				return
			}
		}
	}
}

func authMethods(profile config.Session, keyOnly bool) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod
	paths := []string{}
	if strings.TrimSpace(profile.KeyPath) != "" {
		paths = append(paths, expandHome(profile.KeyPath))
	} else {
		home, _ := os.UserHomeDir()
		for _, name := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
			paths = append(paths, filepath.Join(home, ".ssh", name))
		}
	}
	var keyErrors []string
	for _, path := range paths {
		key, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			keyErrors = append(keyErrors, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			keyErrors = append(keyErrors, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if !keyOnly && profile.Password != "" {
		methods = append(methods, ssh.Password(profile.Password))
	}
	if len(methods) == 0 {
		if len(keyErrors) > 0 {
			return nil, fmt.Errorf("no usable private key: %s", strings.Join(keyErrors, "; "))
		}
		if keyOnly {
			return nil, errors.New("private key not found; set a key path")
		}
		return nil, errors.New("no password or usable private key configured")
	}
	return methods, nil
}

var knownHostsMu sync.Mutex

type KnownHostKey struct {
	Algorithm   string
	Fingerprint string
	Filename    string
	Line        int
}

// HostKeyMismatchError describes either a previously unknown host key or a key
// that differs from all stored keys. Despite the historical name, Expected is
// empty for an unknown host. Both cases require explicit user confirmation.
type HostKeyMismatchError struct {
	Hostname    string
	Address     string
	Algorithm   string
	Fingerprint string
	Expected    []KnownHostKey

	key         ssh.PublicKey
	managerFile string
}

func (e *HostKeyMismatchError) Error() string {
	if len(e.Expected) == 0 {
		return "knownhosts: unknown host key"
	}
	return "knownhosts: key mismatch"
}

func AcceptHostKey(change *HostKeyMismatchError) error {
	if change == nil || change.key == nil || change.managerFile == "" {
		return errors.New("host key change is incomplete")
	}
	return appendKnownHost(change.managerFile, change.Hostname, change.key)
}

func hostKeyCallback() (ssh.HostKeyCallback, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return nil, fmt.Errorf("create ~/.ssh: %w", err)
	}
	managerFile := filepath.Join(home, ".ssh-manager-tui_known_hosts")
	f, err := os.OpenFile(managerFile, os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create host key store: %w", err)
	}
	_ = f.Close()
	if err := os.Chmod(managerFile, 0o600); err != nil {
		return nil, fmt.Errorf("secure host key store: %w", err)
	}
	files := []string{managerFile}
	userFile := filepath.Join(sshDir, "known_hosts")
	if _, err := os.Stat(userFile); err == nil {
		files = append(files, userFile)
	}
	check, err := knownhosts.New(files...)
	if err != nil {
		return nil, fmt.Errorf("read known hosts: %w", err)
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := check(hostname, remote, key)
		if err == nil {
			return nil
		}
		var keyErr *knownhosts.KeyError
		if !errors.As(err, &keyErr) {
			return err
		}
		expected := make([]KnownHostKey, 0, len(keyErr.Want))
		for _, known := range keyErr.Want {
			expected = append(expected, KnownHostKey{
				Algorithm: known.Key.Type(), Fingerprint: ssh.FingerprintSHA256(known.Key),
				Filename: known.Filename, Line: known.Line,
			})
		}
		return &HostKeyMismatchError{
			Hostname: knownhosts.Normalize(hostname), Address: remote.String(),
			Algorithm: key.Type(), Fingerprint: ssh.FingerprintSHA256(key), Expected: expected,
			key: key, managerFile: managerFile,
		}
	}, nil
}

func appendKnownHost(managerFile, hostname string, key ssh.PublicKey) error {
	knownHostsMu.Lock()
	defer knownHostsMu.Unlock()
	line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key)
	if existing, err := os.ReadFile(managerFile); err == nil {
		for _, existingLine := range strings.Split(string(existing), "\n") {
			if strings.TrimSpace(existingLine) == line {
				return nil
			}
		}
	}
	file, err := os.OpenFile(managerFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := fmt.Fprintln(file, line); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

// ReadLines is kept small and bounded for diagnostics returned through the UI.
func ReadLines(r io.Reader, limit int) []string {
	var lines []string
	scanner := bufio.NewScanner(io.LimitReader(r, 64*1024))
	for scanner.Scan() && len(lines) < limit {
		lines = append(lines, scanner.Text())
	}
	return lines
}
