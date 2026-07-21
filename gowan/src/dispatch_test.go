// dispatch_test.go
package main

import (
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

// Runs server_response against a fake dialer and returns the SOCKS5
// reply the client saw plus the backends that were dialed, in order.
func run_dispatch(t *testing.T, fail map[int]bool) (reply []byte, dialed []int) {
	t.Helper()

	var mu sync.Mutex
	old_dial := dial_out
	t.Cleanup(func() { dial_out = old_dial })

	dial_out = func(lb *load_balancer, remote string) (net.Conn, error) {
		mu.Lock()
		defer mu.Unlock()
		idx := -1
		for i, candidate := range lb_list {
			if candidate == lb {
				idx = i
			}
		}
		dialed = append(dialed, idx)
		if fail[idx] {
			return nil, errors.New("connection refused")
		}
		server, _ := net.Pipe()
		return server, nil
	}

	client, proxy := net.Pipe()
	done := make(chan struct{})
	go func() {
		server_response(proxy, "192.0.2.1:443")
		close(done)
	}()

	reply = make([]byte, 10)
	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := client.Read(reply); err != nil {
		t.Fatalf("no SOCKS reply: %v", err)
	}
	client.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server_response did not return")
	}

	mu.Lock()
	defer mu.Unlock()
	return reply, dialed
}

func TestDialSuccessFirstTry(t *testing.T) {
	setup_backends(t, []int{1, 1}, []bool{true, true})

	reply, dialed := run_dispatch(t, nil)
	if reply[1] != SUCCESS {
		t.Fatalf("want SUCCESS reply, got status %d", reply[1])
	}
	if len(dialed) != 1 {
		t.Fatalf("want exactly one dial, got %v", dialed)
	}
}

func TestDialFallbackToNextBackend(t *testing.T) {
	setup_backends(t, []int{1, 1}, []bool{true, true})

	reply, dialed := run_dispatch(t, map[int]bool{0: true})
	if reply[1] != SUCCESS {
		t.Fatalf("want SUCCESS after fallback, got status %d", reply[1])
	}
	if len(dialed) != 2 || dialed[0] != 0 || dialed[1] != 1 {
		t.Fatalf("want dial order [0 1], got %v", dialed)
	}
}

func TestDialAllFailReturnsUnreachable(t *testing.T) {
	setup_backends(t, []int{1, 1}, []bool{true, true})

	reply, dialed := run_dispatch(t, map[int]bool{0: true, 1: true})
	if reply[1] != NETWORK_UNREACHABLE {
		t.Fatalf("want NETWORK_UNREACHABLE, got status %d", reply[1])
	}
	if len(dialed) != 2 {
		t.Fatalf("want one full pass (2 dials), got %v", dialed)
	}
}

func TestDialFallbackSkipsDownBackend(t *testing.T) {
	setup_backends(t, []int{1, 1, 1}, []bool{true, false, true})

	reply, dialed := run_dispatch(t, map[int]bool{0: true})
	if reply[1] != SUCCESS {
		t.Fatalf("want SUCCESS, got status %d", reply[1])
	}
	for _, idx := range dialed {
		if idx == 1 {
			t.Fatalf("fallback dialed DOWN backend 1: %v", dialed)
		}
	}
}
