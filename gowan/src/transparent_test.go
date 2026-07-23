//go:build linux

// transparent_test.go
package main

import (
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

func TestOrigDstFromBytes(t *testing.T) {
	// sockaddr_in: family(2) + port 443 (0x01BB, network order) + 93.184.216.34
	var m [16]byte
	m[0], m[1] = 2, 0
	m[2], m[3] = 0x01, 0xBB
	m[4], m[5], m[6], m[7] = 93, 184, 216, 34

	if got := orig_dst_from_bytes(m); got != "93.184.216.34:443" {
		t.Fatalf("want 93.184.216.34:443, got %s", got)
	}
}

func TestOrigDstPortByteOrder(t *testing.T) {
	var m [16]byte
	m[2], m[3] = 0x1F, 0x90 // 8080
	m[4], m[5], m[6], m[7] = 10, 0, 1, 1

	if got := orig_dst_from_bytes(m); got != "10.0.1.1:8080" {
		t.Fatalf("want 10.0.1.1:8080, got %s", got)
	}
}

// A transparent-mode dispatch must never write SOCKS bytes: on total
// failure the client sees a bare close (EOF), not a SOCKS5 reply.
func TestTransparentDispatchWritesNoSocksBytes(t *testing.T) {
	setup_backends(t, []int{1}, []bool{true})

	var mu sync.Mutex
	old_dial := dial_out
	t.Cleanup(func() { dial_out = old_dial })
	dial_out = func(lb *load_balancer, remote string) (net.Conn, error) {
		mu.Lock()
		defer mu.Unlock()
		return nil, errors.New("connection refused")
	}

	client, proxy := net.Pipe()
	done := make(chan struct{})
	go func() {
		dispatch(proxy, "192.0.2.1:443", "", false, nil)
		close(done)
	}()

	buf := make([]byte, 16)
	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := client.Read(buf)
	if n != 0 || err == nil {
		t.Fatalf("transparent dispatch leaked %d bytes to the client (err=%v)", n, err)
	}
	client.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch_connection did not return")
	}
}
