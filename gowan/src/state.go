// state.go
package main

import (
	"encoding/json"
	"log"
	"net"
	"os"
	"os/exec"
	"sync"
)

// Set from -state-file / -on-change flags before any checker starts.
var state_file string
var on_change_cmd string

// Serializes writers so concurrent observations can't interleave on the
// temp file.
var state_write_mu sync.Mutex

type backend_state struct {
	Name         string `json:"name"`
	IP           string `json:"ip"`
	Iface        string `json:"iface"`
	Ratio        int    `json:"ratio"`
	Status       string `json:"status"`
	Since        int64  `json:"since"`
	ChecksOK     uint64 `json:"checks_ok"`
	ChecksFailed uint64 `json:"checks_failed"`
	TotalConns   uint64 `json:"total_connections"`
	ActiveConns  int64  `json:"active_connections"`
}

func backend_ip(lb *load_balancer) string {
	host, _, err := net.SplitHostPort(lb.address)
	if err != nil {
		return lb.address
	}
	return host
}

func snapshot_backends() []backend_state {
	mutex.Lock()
	defer mutex.Unlock()

	states := make([]backend_state, len(lb_list))
	for idx, lb := range lb_list {
		status := "down"
		if lb.up {
			status = "up"
		}
		states[idx] = backend_state{
			Name:         lb.name,
			IP:           backend_ip(lb),
			Iface:        lb.iface,
			Ratio:        lb.contention_ratio,
			Status:       status,
			Since:        lb.status_since,
			ChecksOK:     lb.checks_ok,
			ChecksFailed: lb.checks_failed,
			TotalConns:   lb.total_conns,
			ActiveConns:  lb.active_conns,
		}
	}
	return states
}

/*
Atomically writes the health state file (write temp + rename) so
readers never see a partial document.
*/
func write_state_file() {
	if state_file == "" {
		return
	}

	state_write_mu.Lock()
	defer state_write_mu.Unlock()

	data, err := json.Marshal(map[string]interface{}{
		"backends": snapshot_backends(),
	})
	if err != nil {
		return
	}
	data = append(data, '\n')

	tmp := state_file + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		log.Println("[WARN] could not write state file:", err)
		return
	}
	if err := os.Rename(tmp, state_file); err != nil {
		log.Println("[WARN] could not write state file:", err)
	}
}

/*
Runs the -on-change hook as '<cmd> <backend-ip> <old-state> <new-state>'.
Fire-and-forget; hook failures are logged, never fatal.
*/
func run_on_change_hook(ip, old_status, new_status string) {
	if on_change_cmd == "" {
		return
	}
	if err := exec.Command(on_change_cmd, ip, old_status, new_status).Run(); err != nil {
		log.Println("[WARN] on-change hook failed:", err)
	}
}
