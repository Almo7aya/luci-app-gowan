// config.go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

/*
Backends file: written by the init script from UCI, re-read on SIGHUP.
Check settings are optional per backend; zero values inherit from the
global -check-* flags.

	{"backends": [
	  {"name": "wan1", "ip": "10.0.1.51", "ratio": 1,
	   "check": {"type": "tcp", "target": "8.8.8.8:53",
	             "interval": 30, "timeout": 5, "fail": 3, "rise": 2}}
	]}
*/
type backend_check_json struct {
	Type     string `json:"type"`
	Target   string `json:"target"`
	Interval int    `json:"interval"`
	Timeout  int    `json:"timeout"`
	Fail     int    `json:"fail"`
	Rise     int    `json:"rise"`
}

type backend_json struct {
	Name  string              `json:"name"`
	IP    string              `json:"ip"`
	Ratio int                 `json:"ratio"`
	Check *backend_check_json `json:"check"`
}

type backends_file_json struct {
	Backends []backend_json `json:"backends"`
}

// The global check settings from flags; per-backend file entries
// override individual fields.
var global_check_cfg check_config

func load_backends_file(path string) ([]backend_json, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var doc backends_file_json
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("invalid backends file: %w", err)
	}
	if len(doc.Backends) == 0 {
		return nil, fmt.Errorf("backends file lists no backends")
	}

	for i := range doc.Backends {
		if doc.Backends[i].Ratio < 1 {
			doc.Backends[i].Ratio = 1
		}
	}
	return doc.Backends, nil
}

/*
Merges a backend's optional check overrides onto the global flag
settings. A backend with no overrides checks exactly like before.
*/
func merged_check_config(b backend_json) check_config {
	cfg := global_check_cfg
	if b.Check == nil {
		return cfg
	}
	if b.Check.Type != "" {
		cfg.ctype = b.Check.Type
	}
	if b.Check.Target != "" {
		cfg.target = b.Check.Target
	}
	if b.Check.Interval > 0 {
		cfg.interval = time.Duration(b.Check.Interval) * time.Second
	}
	if b.Check.Timeout > 0 {
		cfg.timeout = time.Duration(b.Check.Timeout) * time.Second
	}
	if b.Check.Fail > 0 {
		cfg.fail = b.Check.Fail
	}
	if b.Check.Rise > 0 {
		cfg.rise = b.Check.Rise
	}
	return cfg
}
