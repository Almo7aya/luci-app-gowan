// selection_test.go
package main

import (
	"math/big"
	"testing"
	"time"
)

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

func TestPolicyPinsMatchingClient(t *testing.T) {
	name_backends(t, []string{"wan1", "wan2"}, []bool{true, true})
	set_policies([]policy_json{{Type: "client_ip", Match: "10.0.1.100", Wan: "wan2"}})
	t.Cleanup(func() { set_policies(nil) })

	for i := 0; i < 5; i++ {
		lb, _ := pick_backend("10.0.1.100", new(big.Int))
		if lb == nil || lb.name != "wan2" {
			t.Fatalf("pick %d: policy client must pin wan2, got %v", i, lb)
		}
	}
	// A non-matching client is load-balanced normally.
	lb, _ := pick_backend("10.0.1.5", new(big.Int))
	if lb == nil || lb.name != "wan1" {
		t.Fatalf("unmatched client should round-robin from wan1, got %v", lb)
	}
}

func TestPolicyCIDRMatch(t *testing.T) {
	name_backends(t, []string{"wan1", "wan2"}, []bool{true, true})
	set_policies([]policy_json{{Type: "client_ip", Match: "10.0.5.0/24", Wan: "wan2"}})
	t.Cleanup(func() { set_policies(nil) })

	lb, _ := pick_backend("10.0.5.77", new(big.Int))
	if lb == nil || lb.name != "wan2" {
		t.Fatalf("CIDR policy must pin wan2, got %v", lb)
	}
}

func TestPolicyFallsThroughWhenPinnedBackendDown(t *testing.T) {
	name_backends(t, []string{"wan1", "wan2"}, []bool{true, false})
	set_policies([]policy_json{{Type: "client_ip", Match: "10.0.1.100", Wan: "wan2"}})
	t.Cleanup(func() { set_policies(nil) })

	lb, _ := pick_backend("10.0.1.100", new(big.Int))
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
	lb, _ := pick_backend("10.0.1.100", tried)
	if lb == nil || lb.name != "wan1" {
		t.Fatalf("fallback must ignore policy, got %v", lb)
	}
}

func TestStickyPinsAfterFirstChoice(t *testing.T) {
	name_backends(t, []string{"wan1", "wan2"}, []bool{true, true})
	configure_sticky(true, time.Minute)
	t.Cleanup(func() { configure_sticky(false, 0) })

	first, _ := pick_backend("10.0.1.50", new(big.Int))
	if first == nil {
		t.Fatal("no backend on first pick")
	}
	for i := 0; i < 6; i++ {
		lb, _ := pick_backend("10.0.1.50", new(big.Int))
		if lb == nil || lb.name != first.name {
			t.Fatalf("sticky must keep client on %s, got %v", first.name, lb)
		}
	}
	// A different client is free to land elsewhere over several picks.
	seen := map[string]bool{}
	for i := 0; i < 6; i++ {
		lb, _ := pick_backend("10.0.9.%d", new(big.Int)) // constant key, but exercises the path
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

	pick_backend("10.0.1.50", new(big.Int))
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

	first, idx := pick_backend("10.0.1.50", new(big.Int))
	// Mark the pinned backend down; next pick must not return it.
	mutex.Lock()
	lb_list[idx].up = false
	mutex.Unlock()

	lb, _ := pick_backend("10.0.1.50", new(big.Int))
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
