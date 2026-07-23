// udp.go
package main

import (
	"sync"
	"time"
)

/*
Flow table for transparent UDP relaying. Keyed by "client->dst", each
entry owns the sockets for one client/destination pair and is reaped
after a period of inactivity (UDP has no close, so idle timeout is the
only lifetime we get). The socket-facing bits live in udp_linux.go; this
type is platform-independent so its lifecycle logic is unit-tested.
*/

type udp_flow struct {
	lb        *load_balancer
	last_seen time.Time
	closefn   func() // releases sockets + connection accounting
	plat      any    // platform socket bundle, set once at creation
}

type udp_table struct {
	mu      sync.Mutex
	flows   map[string]*udp_flow
	timeout time.Duration
}

func new_udp_table(timeout time.Duration) *udp_table {
	return &udp_table{flows: map[string]*udp_flow{}, timeout: timeout}
}

/*
Returns the flow for key, creating it via create() if absent. The bool
is true when a new flow was created. create() may return nil (e.g. no
healthy backend); nil is not stored.
*/
func (t *udp_table) get_or_create(key string, create func() *udp_flow) (*udp_flow, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if f, ok := t.flows[key]; ok {
		f.last_seen = time.Now()
		return f, false
	}
	f := create()
	if f == nil {
		return nil, false
	}
	f.last_seen = time.Now()
	t.flows[key] = f
	return f, true
}

func (t *udp_table) touch(key string) {
	t.mu.Lock()
	if f, ok := t.flows[key]; ok {
		f.last_seen = time.Now()
	}
	t.mu.Unlock()
}

// Closes and removes flows idle longer than the timeout.
func (t *udp_table) reap() {
	now := time.Now()
	t.mu.Lock()
	var dead []*udp_flow
	for k, f := range t.flows {
		if now.Sub(f.last_seen) > t.timeout {
			dead = append(dead, f)
			delete(t.flows, k)
		}
	}
	t.mu.Unlock()

	for _, f := range dead {
		if f.closefn != nil {
			f.closefn()
		}
	}
}

func (t *udp_table) len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.flows)
}

func (t *udp_table) reap_loop() {
	for {
		time.Sleep(t.timeout)
		t.reap()
	}
}
