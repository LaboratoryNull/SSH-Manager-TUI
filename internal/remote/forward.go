package remote

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"ssh-manager-tui/internal/config"
)

const socksHandshakeTimeout = 15 * time.Second

// Forward is a running SSH port forward. Closing the SSH connection also
// closes all forwards created through it.
type Forward struct {
	owner       *Connection
	ctx         context.Context
	cancel      context.CancelFunc
	spec        config.PortForward
	listener    net.Listener
	address     string
	dialTarget  func(string, string) (net.Conn, error)
	done        chan struct{}
	acceptDone  chan struct{}
	errors      chan error
	closeOnce   sync.Once
	connections map[net.Conn]struct{}
	mu          sync.Mutex
	wg          sync.WaitGroup
}

func (c *Connection) StartForward(spec config.PortForward) (*Forward, error) {
	if c == nil || c.SSH == nil {
		return nil, errors.New("SSH connection is not available")
	}
	select {
	case <-c.done:
		return nil, errors.New("SSH connection is closed")
	default:
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	bindAddress := net.JoinHostPort(spec.BindHost, strconv.Itoa(spec.BindPort))
	var (
		listener net.Listener
		err      error
	)
	switch spec.Type {
	case config.ForwardLocal, config.ForwardDynamic:
		listener, err = net.Listen("tcp", bindAddress)
	case config.ForwardRemote:
		listener, err = c.SSH.Listen("tcp", bindAddress)
	}
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", bindAddress, err)
	}
	forwardContext, cancelForward := context.WithCancel(context.Background())
	forward := &Forward{
		owner: c, spec: spec, listener: listener, address: listener.Addr().String(),
		ctx: forwardContext, cancel: cancelForward,
		done: make(chan struct{}), acceptDone: make(chan struct{}), errors: make(chan error, 1),
		connections: make(map[net.Conn]struct{}),
	}
	if spec.Type == config.ForwardRemote {
		dialer := net.Dialer{Timeout: 15 * time.Second}
		forward.dialTarget = func(network, address string) (net.Conn, error) {
			return dialer.DialContext(forward.ctx, network, address)
		}
	} else {
		forward.dialTarget = func(network, address string) (net.Conn, error) {
			return c.SSH.DialContext(forward.ctx, network, address)
		}
	}
	c.forwardsMu.Lock()
	select {
	case <-c.done:
		c.forwardsMu.Unlock()
		_ = listener.Close()
		cancelForward()
		return nil, errors.New("SSH connection is closed")
	default:
	}
	if c.forwards == nil {
		c.forwards = make(map[*Forward]struct{})
	}
	c.forwards[forward] = struct{}{}
	c.forwardsMu.Unlock()
	go forward.acceptLoop()
	return forward, nil
}

func (f *Forward) Spec() config.PortForward { return f.spec }

func (f *Forward) Address() string { return f.address }

func (f *Forward) Errors() <-chan error { return f.errors }

func (f *Forward) Close() { f.close(true) }

func (f *Forward) close(unregister bool) {
	if f == nil {
		return
	}
	f.closeOnce.Do(func() {
		if f.cancel != nil {
			f.cancel()
		}
		close(f.done)
		_ = f.listener.Close()
		f.mu.Lock()
		connections := make([]net.Conn, 0, len(f.connections))
		for connection := range f.connections {
			connections = append(connections, connection)
		}
		f.mu.Unlock()
		for _, connection := range connections {
			_ = connection.Close()
		}
		if unregister && f.owner != nil {
			f.owner.forwardsMu.Lock()
			delete(f.owner.forwards, f)
			f.owner.forwardsMu.Unlock()
		}
		go func() {
			<-f.acceptDone
			f.wg.Wait()
			close(f.errors)
		}()
	})
}

func (f *Forward) acceptLoop() {
	defer close(f.acceptDone)
	for {
		connection, err := f.listener.Accept()
		if err != nil {
			select {
			case <-f.done:
				return
			default:
				f.reportError(fmt.Errorf("accept %s: %w", f.address, err))
				return
			}
		}
		if !f.track(connection) {
			_ = connection.Close()
			return
		}
		f.wg.Add(1)
		go f.handle(connection)
	}
}

func (f *Forward) handle(incoming net.Conn) {
	defer f.wg.Done()
	defer f.untrack(incoming)
	defer incoming.Close()
	if f.spec.Type == config.ForwardDynamic {
		if err := f.serveSOCKS(incoming); err != nil && !errors.Is(err, net.ErrClosed) {
			return
		}
		return
	}
	target := net.JoinHostPort(f.spec.TargetHost, strconv.Itoa(f.spec.TargetPort))
	outgoing, err := f.dialTarget("tcp", target)
	if err != nil {
		return
	}
	if !f.track(outgoing) {
		_ = outgoing.Close()
		return
	}
	defer f.untrack(outgoing)
	defer outgoing.Close()
	relay(incoming, outgoing)
}

func (f *Forward) serveSOCKS(client net.Conn) error {
	_ = client.SetDeadline(time.Now().Add(socksHandshakeTimeout))
	header := make([]byte, 2)
	if _, err := io.ReadFull(client, header); err != nil {
		return err
	}
	if header[0] != 5 {
		return errors.New("unsupported SOCKS version")
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(client, methods); err != nil {
		return err
	}
	noAuth := false
	for _, method := range methods {
		if method == 0 {
			noAuth = true
			break
		}
	}
	if !noAuth {
		_, _ = client.Write([]byte{5, 0xff})
		return errors.New("SOCKS client does not support no-auth mode")
	}
	if _, err := client.Write([]byte{5, 0}); err != nil {
		return err
	}
	request := make([]byte, 4)
	if _, err := io.ReadFull(client, request); err != nil {
		return err
	}
	if request[0] != 5 || request[1] != 1 {
		_ = writeSOCKSReply(client, 7)
		return errors.New("only SOCKS CONNECT is supported")
	}
	host, err := readSOCKSHost(client, request[3])
	if err != nil {
		_ = writeSOCKSReply(client, 8)
		return err
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(client, portBytes); err != nil {
		return err
	}
	target := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(portBytes))))
	server, err := f.dialTarget("tcp", target)
	if err != nil {
		_ = writeSOCKSReply(client, 5)
		return err
	}
	if !f.track(server) {
		_ = server.Close()
		return net.ErrClosed
	}
	defer f.untrack(server)
	defer server.Close()
	if err := writeSOCKSReply(client, 0); err != nil {
		return err
	}
	_ = client.SetDeadline(time.Time{})
	relay(client, server)
	return nil
}

func readSOCKSHost(reader io.Reader, addressType byte) (string, error) {
	switch addressType {
	case 1:
		address := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(reader, address); err != nil {
			return "", err
		}
		return net.IP(address).String(), nil
	case 3:
		length := []byte{0}
		if _, err := io.ReadFull(reader, length); err != nil {
			return "", err
		}
		address := make([]byte, int(length[0]))
		if _, err := io.ReadFull(reader, address); err != nil {
			return "", err
		}
		return string(address), nil
	case 4:
		address := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(reader, address); err != nil {
			return "", err
		}
		return net.IP(address).String(), nil
	default:
		return "", fmt.Errorf("unsupported SOCKS address type %d", addressType)
	}
}

func writeSOCKSReply(writer io.Writer, status byte) error {
	_, err := writer.Write([]byte{5, status, 0, 1, 0, 0, 0, 0, 0, 0})
	return err
}

func relay(left, right net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	copySide := func(destination, source net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(destination, source)
		if closer, ok := destination.(interface{ CloseWrite() error }); ok {
			_ = closer.CloseWrite()
		} else {
			_ = destination.Close()
		}
	}
	go copySide(left, right)
	go copySide(right, left)
	wg.Wait()
}

func (f *Forward) track(connection net.Conn) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	select {
	case <-f.done:
		return false
	default:
		f.connections[connection] = struct{}{}
		return true
	}
}

func (f *Forward) untrack(connection net.Conn) {
	f.mu.Lock()
	delete(f.connections, connection)
	f.mu.Unlock()
}

func (f *Forward) reportError(err error) {
	select {
	case f.errors <- err:
	case <-f.done:
	default:
	}
}
