// api.go
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

var start_time = time.Now()

type api_status struct {
	UptimeSeconds     int64           `json:"uptime_seconds"`
	TotalConnections  uint64          `json:"total_connections"`
	ActiveConnections int64           `json:"active_connections"`
	Backends          []backend_state `json:"backends"`
}

/*
Serves GET /status on the given address (expected to be localhost —
exposure beyond that is a deliberate firewall decision, not a default).
*/
func start_status_api(addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", status_handler)

	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil {
			log.Println("[WARN] status API stopped:", err)
		}
	}()
	log.Println("[INFO] Status API on http://" + addr + "/status")
}

func status_handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	backends := snapshot_backends()

	resp := api_status{
		UptimeSeconds: int64(time.Since(start_time).Seconds()),
		Backends:      backends,
	}
	for _, b := range backends {
		resp.TotalConnections += b.TotalConns
		resp.ActiveConnections += b.ActiveConns
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
