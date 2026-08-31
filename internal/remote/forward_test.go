package remote

import (
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"ssh-manager-tui/internal/config"
)

func TestTCPForwardRelaysTraffic(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		connection, acceptErr := echo.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		_, _ = io.Copy(connection, connection)
	}()
	targetHost, targetPortText, _ := net.SplitHostPort(echo.Addr().String())
	targetPort, _ := strconv.Atoi(targetPortText)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	forward := &Forward{
		spec:     config.PortForward{Type: config.ForwardLocal, TargetHost: targetHost, TargetPort: targetPort},
		listener: listener, address: listener.Addr().String(), dialTarget: net.Dial,
		done: make(chan struct{}), acceptDone: make(chan struct{}), errors: make(chan error, 1),
		connections: make(map[net.Conn]struct{}),
	}
	go forward.acceptLoop()
	defer forward.Close()
	client, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	payload := []byte("forwarded")
	if _, err := client.Write(payload); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(client, got); err != nil || string(got) != string(payload) {
		t.Fatalf("relayed payload = %q, err = %v", got, err)
	}
}

func TestSOCKS5ConnectRelaysTraffic(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		connection, acceptErr := echo.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		_, _ = io.Copy(connection, connection)
	}()

	client, server := net.Pipe()
	defer client.Close()
	forward := &Forward{
		done: make(chan struct{}), connections: make(map[net.Conn]struct{}),
		dialTarget: net.Dial,
	}
	result := make(chan error, 1)
	go func() { result <- forward.serveSOCKS(server) }()
	defer server.Close()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))

	if _, err := client.Write([]byte{5, 1, 0}); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 2)
	if _, err := io.ReadFull(client, response); err != nil || response[0] != 5 || response[1] != 0 {
		t.Fatalf("method response = %v, err = %v", response, err)
	}
	host, portText, err := net.SplitHostPort(echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portText)
	request := append([]byte{5, 1, 0, 3, byte(len(host))}, []byte(host)...)
	request = binary.BigEndian.AppendUint16(request, uint16(port))
	if _, err := client.Write(request); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(client, reply); err != nil || reply[1] != 0 {
		t.Fatalf("connect reply = %v, err = %v", reply, err)
	}
	payload := []byte("through-ssh-socks")
	if _, err := client.Write(payload); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(client, got); err != nil || string(got) != string(payload) {
		t.Fatalf("relayed payload = %q, err = %v", got, err)
	}
	_ = client.Close()
	select {
	case <-result:
	case <-time.After(3 * time.Second):
		t.Fatal("SOCKS relay did not stop")
	}
}

func TestSOCKS5RejectsAuthenticationRequirement(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	forward := &Forward{done: make(chan struct{}), connections: make(map[net.Conn]struct{})}
	result := make(chan error, 1)
	go func() { result <- forward.serveSOCKS(server) }()
	if _, err := client.Write([]byte{5, 1, 2}); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatal(err)
	}
	if reply[1] != 0xff {
		t.Fatalf("authentication reply = %v", reply)
	}
	if err := <-result; err == nil {
		t.Fatal("expected unsupported authentication error")
	}
}
