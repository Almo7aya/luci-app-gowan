// state_test.go
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteStateFile(t *testing.T) {
	setup_backends(t, []int{1, 2}, []bool{true, false})
	lb_list[0].address = "10.0.1.21:0"
	lb_list[1].address = "10.0.1.22:0"
	lb_list[1].checks_failed = 7

	old_state := state_file
	t.Cleanup(func() { state_file = old_state })
	state_file = filepath.Join(t.TempDir(), "health.json")

	write_state_file()

	data, err := os.ReadFile(state_file)
	if err != nil {
		t.Fatalf("state file not written: %v", err)
	}

	var doc struct {
		Backends []backend_state `json:"backends"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("state file is not valid JSON: %v", err)
	}

	if len(doc.Backends) != 2 {
		t.Fatalf("want 2 backends, got %d", len(doc.Backends))
	}
	if doc.Backends[0].IP != "10.0.1.21" || doc.Backends[0].Status != "up" {
		t.Fatalf("backend 0 wrong: %+v", doc.Backends[0])
	}
	if doc.Backends[1].IP != "10.0.1.22" || doc.Backends[1].Status != "down" {
		t.Fatalf("backend 1 wrong: %+v", doc.Backends[1])
	}
	if doc.Backends[1].Ratio != 2 || doc.Backends[1].ChecksFailed != 7 {
		t.Fatalf("backend 1 counters wrong: %+v", doc.Backends[1])
	}
}

func TestWriteStateFileDisabled(t *testing.T) {
	old_state := state_file
	t.Cleanup(func() { state_file = old_state })
	state_file = ""

	// Must be a no-op, not a crash.
	write_state_file()
}
