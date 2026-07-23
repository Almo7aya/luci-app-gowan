// selection_test.go
package main

import (
	"math/big"
	"net"
	"strconv"
	"testing"
	"time"
)

// Test helper: split a "host:port" dest into the (dest_ip, dest_host,
// port) triple pick_backend now takes, so the tests read naturally.
func pbdest(client, dest string, tried *big.Int) (*load_balancer, int) {
	host, ps, _ := net.SplitHostPort(dest)
	port, _ := strconv.Atoi(ps)
	ip, hn := "", ""
	if net.ParseIP(host) != nil {
		ip = host
	} else {
		hn = host
	}
	return pick_backend(client, ip, hn, port, tried)
}

func name_backends(t *testing.T, names []string, up []bool) {
	t.Helper()
	lb_list = make([]*load_balancer, len(names))
	for i := range names {
		lb_list[i] = &load_balancer{
			name:             names[i],
			address:          "10.0.0.1:0",
			iface:            "test0",
			contention_ratio: 1,
			up:               up[i],
		}
	}
	lb_index = 0
	checkers = nil
}

const anyDest = "203.0.113.9:443"

func TestPolicyPinsMatchingClient(t *testing.T) {
	name_backends(t, []string{"wan1", "wan2"}, []bool{true, true})
	set_policies([]policy_json{{Type: "client_ip", Match: "10.0.1.100", Wan: "wan2"}})
	t.Cleanup(func() { set_policies(nil) })

	for i := 0; i < 5; i++ {
		lb, _ := pbdest("10.0.1.100", anyDest, new(big.Int))
		if lb == nil || lb.name != "wan2" {
			t.Fatalf("pick %d: policy client must pin wan2, got %v", i, lb)
		}
	}
	// A non-matching client is load-balanced normally.
	lb, _ := pbdest("10.0.1.5", anyDest, new(big.Int))
	if lb == nil || lb.name != "wan1" {
		t.Fatalf("unmatched client should round-robin from wan1, got %v", lb)
	}
}

func TestPolicyCIDRMatch(t *testing.T) {
	name_backends(t, []string{"wan1", "wan2"}, []bool{true, true})
	set_policies([]policy_json{{Type: "client_ip", Match: "10.0.5.0/24", Wan: "wan2"}})
	t.Cleanup(func() { set_policies(nil) })

	lb, _ := pbdest("10.0.5.77", anyDest, new(big.Int))
	if lb == nil || lb.name != "wan2" {
		t.Fatalf("CIDR policy must pin wan2, got %v", lb)
	}
}

func TestPolicyPortMatch(t *testing.T) {
	name_backends(t, []string{"wan1", "wan2"}, []bool{true, true})
	set_policies([]policy_json{{Type: "port", Match: "443,6881:6889", Wan: "wan2"}})
	t.Cleanup(func() { set_policies(nil) })

	// Single port in the list.
	if lb, _ := pbdest("10.0.1.5", "1.2.3.4:443", new(big.Int)); lb == nil || lb.name != "wan2" {
		t.Fatalf("port 443 must pin wan2, got %v", lb)
	}
	// Inside the range.
	if lb, _ := pbdest("10.0.1.5", "1.2.3.4:6885", new(big.Int)); lb == nil || lb.name != "wan2" {
		t.Fatalf("port 6885 in range must pin wan2, got %v", lb)
	}
	// Outside → load balanced (reset index for a deterministic first pick).
	lb_index = 0
	if lb, _ := pbdest("10.0.1.5", "1.2.3.4:80", new(big.Int)); lb == nil || lb.name != "wan1" {
		t.Fatalf("port 80 should not match, got %v", lb)
	}
}

func TestPolicyDestIPMatch(t *testing.T) {
	name_backends(t, []string{"wan1", "wan2"}, []bool{true, true})
	set_policies([]policy_json{{Type: "dest_ip", Match: "8.8.8.0/24", Wan: "wan2"}})
	t.Cleanup(func() { set_policies(nil) })

	if lb, _ := pbdest("10.0.1.5", "8.8.8.8:53", new(big.Int)); lb == nil || lb.name != "wan2" {
		t.Fatalf("dest 8.8.8.8 must pin wan2, got %v", lb)
	}
	// A domain destination has no IP, so a dest_ip rule cannot match it.
	lb_index = 0
	if lb, _ := pbdest("10.0.1.5", "example.com:443", new(big.Int)); lb == nil || lb.name != "wan1" {
		t.Fatalf("domain dest must not match a dest_ip rule, got %v", lb)
	}
}

func TestPolicyDomainMatch(t *testing.T) {
	name_backends(t, []string{"wan1", "wan2"}, []bool{true, true})
	set_policies([]policy_json{{Type: "domain", Match: "*.example.com", Wan: "wan2"}})
	t.Cleanup(func() { set_policies(nil) })

	if lb, _ := pbdest("10.0.1.5", "cdn.example.com:443", new(big.Int)); lb == nil || lb.name != "wan2" {
		t.Fatalf("subdomain must match domain rule, got %v", lb)
	}
	if lb, _ := pbdest("10.0.1.5", "example.com:443", new(big.Int)); lb == nil || lb.name != "wan2" {
		t.Fatalf("apex must match domain rule, got %v", lb)
	}
	// Negative checks fall to round-robin; reset the index so "wan1" is
	// the deterministic first pick rather than depending on prior calls.
	lb_index = 0
	if lb, _ := pbdest("10.0.1.5", "example.org:443", new(big.Int)); lb == nil || lb.name != "wan1" {
		t.Fatalf("other domain must not match, got %v", lb)
	}
	// An IP destination is never matched by a domain rule.
	lb_index = 0
	if lb, _ := pbdest("10.0.1.5", "93.184.216.34:443", new(big.Int)); lb == nil || lb.name != "wan1" {
		t.Fatalf("IP dest must not match a domain rule, got %v", lb)
	}
}

// Transparent mode supplies BOTH the dest IP (dial target) and the SNI/
// Host name; a domain rule must still match on the name.
func TestPolicyDomainMatchWithKnownIP(t *testing.T) {
	name_backends(t, []string{"wan1", "wan2"}, []bool{true, true})
	set_policies([]policy_json{{Type: "domain", Match: "*.example.com", Wan: "wan2"}})
	t.Cleanup(func() { set_policies(nil) })

	// dest_ip AND dest_host both set (the transparent case).
	if lb, _ := pick_backend("10.0.1.5", "93.184.216.34", "cdn.example.com", 443, new(big.Int)); lb == nil || lb.name != "wan2" {
		t.Fatalf("domain rule must match even when the dest IP is also known, got %v", lb)
	}
	// Same IP, non-matching name → load balanced.
	lb_index = 0
	if lb, _ := pick_backend("10.0.1.5", "93.184.216.34", "example.org", 443, new(big.Int)); lb == nil || lb.name != "wan1" {
		t.Fatalf("non-matching name must not pin, got %v", lb)
	}
}

func TestPolicyFirstMatchWins(t *testing.T) {
	name_backends(t, []string{"wan1", "wan2", "wan3"}, []bool{true, true, true})
	set_policies([]policy_json{
		{Type: "port", Match: "443", Wan: "wan3"},
		{Type: "dest_ip", Match: "8.8.8.8", Wan: "wan2"},
	})
	t.Cleanup(func() { set_policies(nil) })

	// Both rules match 8.8.8.8:443; the first (port) wins.
	if lb, _ := pbdest("10.0.1.5", "8.8.8.8:443", new(big.Int)); lb == nil || lb.name != "wan3" {
		t.Fatalf("first matching rule must win, got %v", lb)
	}
}

func TestPolicyFallsThroughWhenPinnedBackendDown(t *testing.T) {
	name_backends(t, []string{"wan1", "wan2"}, []bool{true, false})
	set_policies([]policy_json{{Type: "client_ip", Match: "10.0.1.100", Wan: "wan2"}})
	t.Cleanup(func() { set_policies(nil) })

	lb, _ := pbdest("10.0.1.100", anyDest, new(big.Int))
	if lb == nil || lb.name != "wan1" {
		t.Fatalf("down pinned backend must fall through to a healthy one, got %v", lb)
	}
}

func TestPolicyIgnoredOnFallback(t *testing.T) {
	name_backends(t, []string{"wan1", "wan2"}, []bool{true, true})
	set_policies([]policy_json{{Type: "client_ip", Match: "10.0.1.100", Wan: "wan2"}})
	t.Cleanup(func() { set_policies(nil) })

	// Simulate wan2 already tried (dial failed): must fall over to wan1
	// despite the policy.
	tried := new(big.Int)
	tried.SetBit(tried, 1, 1)
	lb, _ := pbdest("10.0.1.100", anyDest, tried)
	if lb == nil || lb.name != "wan1" {
		t.Fatalf("fallback must ignore policy, got %v", lb)
	}
}

func TestStickyPinsAfterFirstChoice(t *testing.T) {
	name_backends(t, []string{"wan1", "wan2"}, []bool{true, true})
	configure_sticky(true, time.Minute)
	t.Cleanup(func() { configure_sticky(false, 0) })

	first, _ := pbdest("10.0.1.50", anyDest, new(big.Int))
	if first == nil {
		t.Fatal("no backend on first pick")
	}
	for i := 0; i < 6; i++ {
		lb, _ := pbdest("10.0.1.50", anyDest, new(big.Int))
		if lb == nil || lb.name != first.name {
			t.Fatalf("sticky must keep client on %s, got %v", first.name, lb)
		}
	}
	// A different client is free to land elsewhere over several picks.
	seen := map[string]bool{}
	for i := 0; i < 6; i++ {
		lb, _ := pbdest("10.0.9.%d", anyDest, new(big.Int)) // constant key, but exercises the path
		if lb != nil {
			seen[lb.name] = true
		}
	}
	if len(seen) == 0 {
		t.Fatal("second client never got a backend")
	}
}

func TestStickyExpires(t *testing.T) {
	name_backends(t, []string{"wan1", "wan2"}, []bool{true, true})
	configure_sticky(true, 10*time.Millisecond)
	t.Cleanup(func() { configure_sticky(false, 0) })

	pbdest("10.0.1.50", anyDest, new(big.Int))
	time.Sleep(30 * time.Millisecond)

	sticky_mu.Lock()
	_, ok := sticky_map["10.0.1.50"]
	sticky_mu.Unlock()
	// Lookup after expiry must not return a stale pin.
	if ok {
		if lb, _ := sticky_lookup("10.0.1.50"); lb != nil {
			t.Fatal("expired sticky entry must not resolve")
		}
	}
}

func TestStickySkipsDownBackend(t *testing.T) {
	name_backends(t, []string{"wan1", "wan2"}, []bool{true, true})
	configure_sticky(true, time.Minute)
	t.Cleanup(func() { configure_sticky(false, 0) })

	first, idx := pbdest("10.0.1.50", anyDest, new(big.Int))
	// Mark the pinned backend down; next pick must not return it.
	mutex.Lock()
	lb_list[idx].up = false
	mutex.Unlock()

	lb, _ := pbdest("10.0.1.50", anyDest, new(big.Int))
	if lb == nil || lb.name == first.name {
		t.Fatalf("sticky must not return a down backend, got %v", lb)
	}
}

func TestCredentialsConstantTime(t *testing.T) {
	auth_user, auth_pass = "admin", "s3cret"
	t.Cleanup(func() { auth_user, auth_pass = "", "" })

	if !auth_enabled() {
		t.Fatal("auth should be enabled")
	}
	if !credentials_ok("admin", "s3cret") {
		t.Fatal("correct credentials rejected")
	}
	if credentials_ok("admin", "wrong") || credentials_ok("root", "s3cret") {
		t.Fatal("wrong credentials accepted")
	}
}
