// selector_test.go
package main

import (
	"math/big"
	"testing"
)

// Installs a fresh backend list for a test and resets selector state.
func setup_backends(t *testing.T, ratios []int, up []bool) {
	t.Helper()
	lb_list = make([]*load_balancer, len(ratios))
	for i := range ratios {
		lb_list[i] = &load_balancer{
			address:          "10.0.0.1:0",
			iface:            "test0",
			contention_ratio: ratios[i],
			up:               up[i],
		}
	}
	lb_index = 0
	checkers = nil
}

func pick_sequence(t *testing.T, n int) []int {
	t.Helper()
	seq := make([]int, n)
	for i := 0; i < n; i++ {
		lb, idx := get_load_balancer(nil)
		if lb == nil {
			t.Fatalf("pick %d: got nil balancer", i)
		}
		seq[i] = idx
	}
	return seq
}

func TestContentionRatioWeighting(t *testing.T) {
	setup_backends(t, []int{1, 2}, []bool{true, true})

	got := pick_sequence(t, 6)
	want := []int{0, 1, 1, 0, 1, 1}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sequence %v, want %v", got, want)
		}
	}
}

func TestDownBackendIsSkipped(t *testing.T) {
	setup_backends(t, []int{1, 1, 1}, []bool{true, false, true})

	for i, idx := range pick_sequence(t, 10) {
		if idx == 1 {
			t.Fatalf("pick %d selected DOWN backend 1", i)
		}
	}
}

func TestAllDownGuardKeepsServing(t *testing.T) {
	setup_backends(t, []int{1, 1}, []bool{false, false})

	seen := map[int]bool{}
	for _, idx := range pick_sequence(t, 4) {
		seen[idx] = true
	}
	if !seen[0] || !seen[1] {
		t.Fatalf("all-down guard must round-robin every backend, saw %v", seen)
	}
}

func TestExcludedBackendIsSkipped(t *testing.T) {
	setup_backends(t, []int{1, 1}, []bool{true, true})

	exclude := new(big.Int)
	exclude.SetBit(exclude, 0, 1)

	for i := 0; i < 4; i++ {
		lb, idx := get_load_balancer(exclude)
		if lb == nil || idx != 1 {
			t.Fatalf("pick %d: want backend 1, got %d", i, idx)
		}
	}
}

func TestAllExcludedReturnsNil(t *testing.T) {
	setup_backends(t, []int{1, 1}, []bool{true, true})

	exclude := new(big.Int)
	exclude.SetBit(exclude, 0, 1)
	exclude.SetBit(exclude, 1, 1)

	if lb, idx := get_load_balancer(exclude); lb != nil || idx != -1 {
		t.Fatalf("want (nil, -1) when every backend is excluded, got (%v, %d)", lb, idx)
	}
}

func TestExclusionBeatsAllDownGuard(t *testing.T) {
	// Backends may be DOWN-but-eligible (all-down guard), yet an already
	// tried backend must never be retried within the same connection.
	setup_backends(t, []int{1, 1}, []bool{false, false})

	exclude := new(big.Int)
	exclude.SetBit(exclude, 0, 1)

	for i := 0; i < 3; i++ {
		lb, idx := get_load_balancer(exclude)
		if lb == nil || idx != 1 {
			t.Fatalf("pick %d: want backend 1, got %d", i, idx)
		}
	}
}
