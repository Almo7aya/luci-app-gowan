// reload_test.go
package main

import (
	"encoding/json"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Finds an IPv4 address on a real, up, non-loopback interface — what
// apply_backends can resolve. Skips the test on machines without one.
func local_test_ip(t *testing.T) string {
	t.Helper()
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	t.Skip("no usable non-loopback IPv4 interface")
	return ""
}

func write_backends_json(t *testing.T, doc backends_file_json) string {
	t.Helper()
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "backends.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadBackendsFile(t *testing.T) {
	path := write_backends_json(t, backends_file_json{Backends: []backend_json{
		{Name: "wan1", IP: "10.0.1.51", Ratio: 2,
			Check: &backend_check_json{Type: "http", Target: "http://example.com", Fail: 5}},
		{Name: "wan2", IP: "10.0.1.52"},
	}})

	list, err := load_backends_file(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 backends, got %d", len(list))
	}
	if list[1].Ratio != 1 {
		t.Fatalf("missing ratio must clamp to 1, got %d", list[1].Ratio)
	}
}

func TestLoadBackendsFileRejectsEmpty(t *testing.T) {
	path := write_backends_json(t, backends_file_json{})
	if _, err := load_backends_file(path); err == nil {
		t.Fatal("empty backends file must be an error")
	}
}

func TestMergedCheckConfigInheritsGlobals(t *testing.T) {
	old := global_check_cfg
	t.Cleanup(func() { global_check_cfg = old })
	global_check_cfg = check_config{
		ctype: "tcp", target: "8.8.8.8:53",
		interval: 30 * time.Second, timeout: 5 * time.Second, fail: 3, rise: 2,
	}

	// No overrides: identical to globals.
	cfg := merged_check_config(backend_json{IP: "10.0.0.1"})
	if cfg != global_check_cfg {
		t.Fatalf("no-override merge changed config: %+v", cfg)
	}

	// Partial override: only the given fields change.
	cfg = merged_check_config(backend_json{IP: "10.0.0.1",
		Check: &backend_check_json{Type: "http", Target: "http://x/", Fail: 5}})
	if cfg.ctype != "http" || cfg.target != "http://x/" || cfg.fail != 5 {
		t.Fatalf("overrides not applied: %+v", cfg)
	}
	if cfg.interval != 30*time.Second || cfg.rise != 2 {
		t.Fatalf("non-overridden fields must inherit: %+v", cfg)
	}
}

func TestApplyBackendsReusesSurvivors(t *testing.T) {
	ip := local_test_ip(t)

	old_cfg := global_check_cfg
	t.Cleanup(func() {
		global_check_cfg = old_cfg
		stop_health_checks()
		lb_list = nil
	})
	global_check_cfg = check_config{ctype: "none"}
	lb_list = nil
	lb_index = 0

	if n := apply_backends([]backend_json{{Name: "a", IP: ip, Ratio: 1}}); n != 1 {
		t.Fatalf("initial apply: want 1, got %d", n)
	}

	// Accumulate identifying state on the survivor.
	mutex.Lock()
	survivor := lb_list[0]
	survivor.up = false
	survivor.checks_failed = 9
	survivor.total_conns = 42
	mutex.Unlock()

	if n := apply_backends([]backend_json{{Name: "a2", IP: ip, Ratio: 3}}); n != 1 {
		t.Fatalf("re-apply: want 1, got %d", n)
	}

	mutex.Lock()
	defer mutex.Unlock()
	if lb_list[0] != survivor {
		t.Fatal("same-IP backend must reuse the existing struct")
	}
	if survivor.contention_ratio != 3 || survivor.name != "a2" {
		t.Fatalf("ratio/name not updated: %+v", survivor)
	}
	if survivor.up || survivor.checks_failed != 9 || survivor.total_conns != 42 {
		t.Fatalf("health/traffic state must survive reload: %+v", survivor)
	}
}

func TestApplyBackendsKeepsCurrentSetOnGarbage(t *testing.T) {
	ip := local_test_ip(t)

	old_cfg := global_check_cfg
	t.Cleanup(func() {
		global_check_cfg = old_cfg
		stop_health_checks()
		lb_list = nil
	})
	global_check_cfg = check_config{ctype: "none"}
	lb_list = nil

	apply_backends([]backend_json{{Name: "a", IP: ip, Ratio: 1}})

	// Nothing usable: unknown IP and garbage.
	if n := apply_backends([]backend_json{{IP: "203.0.113.99"}, {IP: "not-an-ip"}}); n != 0 {
		t.Fatalf("unusable file must return 0, got %d", n)
	}

	mutex.Lock()
	defer mutex.Unlock()
	if len(lb_list) != 1 || backend_ip(lb_list[0]) != ip {
		t.Fatal("current backend set must be kept when the new file is unusable")
	}
}

func TestStatusAPIHandler(t *testing.T) {
	setup_backends(t, []int{1, 2}, []bool{true, false})
	lb_list[0].name = "wan1"
	lb_list[0].total_conns = 10
	lb_list[0].active_conns = 2
	lb_list[1].name = "wan2"
	lb_list[1].total_conns = 5

	req := httptest.NewRequest("GET", "/status", nil)
	rec := httptest.NewRecorder()
	status_handler(rec, req)

	if rec.Code != 200 {
		t.Fatalf("want 200, got %d", rec.Code)
	}

	var doc api_status
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("status response is not valid JSON: %v", err)
	}
	if len(doc.Backends) != 2 {
		t.Fatalf("want 2 backends, got %d", len(doc.Backends))
	}
	if doc.TotalConnections != 15 || doc.ActiveConnections != 2 {
		t.Fatalf("totals wrong: %+v", doc)
	}
	if doc.Backends[0].Name != "wan1" || doc.Backends[1].Status != "down" {
		t.Fatalf("backend fields wrong: %+v", doc.Backends)
	}
}

func TestStatusAPIRejectsNonGet(t *testing.T) {
	req := httptest.NewRequest("POST", "/status", nil)
	rec := httptest.NewRecorder()
	status_handler(rec, req)
	if rec.Code != 405 {
		t.Fatalf("want 405 for POST, got %d", rec.Code)
	}
}
