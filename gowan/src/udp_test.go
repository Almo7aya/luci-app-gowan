// udp_test.go
package main

import (
	"testing"
	"time"
)

func TestUDPTableCreateAndReuse(t *testing.T) {
	tbl := new_udp_table(time.Minute)

	creates := 0
	mk := func() *udp_flow { creates++; return &udp_flow{} }

	f1, created1 := tbl.get_or_create("a->b", mk)
	if f1 == nil || !created1 {
		t.Fatal("first get_or_create must create")
	}
	f2, created2 := tbl.get_or_create("a->b", mk)
	if f2 != f1 || created2 {
		t.Fatal("same key must reuse the existing flow")
	}
	if creates != 1 {
		t.Fatalf("create called %d times, want 1", creates)
	}
	if tbl.len() != 1 {
		t.Fatalf("table len %d, want 1", tbl.len())
	}
}

func TestUDPTableNilCreateNotStored(t *testing.T) {
	tbl := new_udp_table(time.Minute)
	f, created := tbl.get_or_create("x->y", func() *udp_flow { return nil })
	if f != nil || created {
		t.Fatal("nil flow must not be stored")
	}
	if tbl.len() != 0 {
		t.Fatal("nil flow must leave the table empty")
	}
}

func TestUDPTableReapClosesIdle(t *testing.T) {
	tbl := new_udp_table(20 * time.Millisecond)

	closed := make(chan struct{}, 1)
	tbl.get_or_create("a->b", func() *udp_flow {
		return &udp_flow{closefn: func() { closed <- struct{}{} }}
	})

	// Not yet idle.
	tbl.reap()
	if tbl.len() != 1 {
		t.Fatal("flow reaped too early")
	}

	time.Sleep(40 * time.Millisecond)
	tbl.reap()
	if tbl.len() != 0 {
		t.Fatal("idle flow must be reaped")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("closefn was not called on reap")
	}
}

func TestUDPTableTouchKeepsAlive(t *testing.T) {
	tbl := new_udp_table(30 * time.Millisecond)
	tbl.get_or_create("a->b", func() *udp_flow { return &udp_flow{} })

	// Keep touching faster than the timeout; must survive.
	for i := 0; i < 5; i++ {
		time.Sleep(10 * time.Millisecond)
		tbl.touch("a->b")
		tbl.reap()
	}
	if tbl.len() != 1 {
		t.Fatal("touched flow must not be reaped")
	}
}
