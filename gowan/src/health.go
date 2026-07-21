// health.go
package main

import (
	"log"
	"net/http"
	"sync"
	"time"
)

type check_config struct {
	ctype    string // "tcp" or "http" ("none" never reaches the checker)
	target   string // host:port for tcp, URL for http
	interval time.Duration
	timeout  time.Duration
	fail     int
	rise     int
}

/*
Pure fail/rise threshold state machine. Starts UP (optimistic) so
traffic flows before the first check completes.
*/
type health_state struct {
	fail_threshold int
	rise_threshold int
	up             bool
	consec_fail    int
	consec_ok      int
}

func new_health_state(fail, rise int) *health_state {
	if fail < 1 {
		fail = 1
	}
	if rise < 1 {
		rise = 1
	}
	return &health_state{
		fail_threshold: fail,
		rise_threshold: rise,
		up:             true,
	}
}

/*
Records one check result and reports whether the UP/DOWN verdict
flipped on this observation.
*/
func (s *health_state) observe(ok bool) bool {
	if ok {
		s.consec_fail = 0
		s.consec_ok++
		if !s.up && s.consec_ok >= s.rise_threshold {
			s.up = true
			return true
		}
		return false
	}

	s.consec_ok = 0
	s.consec_fail++
	if s.up && s.consec_fail >= s.fail_threshold {
		s.up = false
		return true
	}
	return false
}

type health_checker struct {
	lb    *load_balancer
	cfg   check_config
	mu    sync.Mutex
	state *health_state
	check func() bool // substituted in tests
}

var checkers []*health_checker

func start_health_checks(cfg check_config) {
	checkers = make([]*health_checker, len(lb_list))
	for idx, lb := range lb_list {
		hc := &health_checker{
			lb:    lb,
			cfg:   cfg,
			state: new_health_state(cfg.fail, cfg.rise),
		}
		hc.check = hc.run_check
		checkers[idx] = hc
		go hc.loop()
	}
	log.Printf("[INFO] Health checks started: %s %s every %s (fail %d / rise %d)\n",
		cfg.ctype, cfg.target, cfg.interval, cfg.fail, cfg.rise)
}

func checker_for(lb *load_balancer) *health_checker {
	for _, hc := range checkers {
		if hc != nil && hc.lb == lb {
			return hc
		}
	}
	return nil
}

/*
Feeds a live dial failure into the backend's health state so real
outages converge faster than the periodic check interval.
*/
func (lb *load_balancer) note_dial_failure() {
	if hc := checker_for(lb); hc != nil {
		go hc.observe(false)
	}
}

func (hc *health_checker) loop() {
	for {
		hc.observe(hc.check())
		time.Sleep(hc.cfg.interval)
	}
}

/*
Feeds one result (periodic check or live dial failure) into the state
machine and publishes flips: backend flag, counters, state file, hook.
*/
func (hc *health_checker) observe(ok bool) {
	hc.mu.Lock()
	flipped := hc.state.observe(ok)
	up := hc.state.up
	hc.mu.Unlock()

	mutex.Lock()
	if ok {
		hc.lb.checks_ok++
	} else {
		hc.lb.checks_failed++
	}
	if flipped {
		hc.lb.up = up
		hc.lb.status_since = time.Now().Unix()
	}
	all_down := true
	for _, lb := range lb_list {
		if lb.up {
			all_down = false
			break
		}
	}
	mutex.Unlock()

	if !flipped {
		// Keep the counters in the state file fresh for LuCI; it lives
		// on tmpfs, so frequent rewrites cost nothing.
		go write_state_file()
		return
	}

	old_status, new_status := "up", "down"
	if up {
		old_status, new_status = "down", "up"
	}
	log.Println("[INFO] backend", hc.lb.address, "is now", new_status, "(was", old_status+")")
	if all_down {
		log.Println("[WARN] ALL backends are DOWN — continuing with the full backend set")
	}

	go func(ip, old_s, new_s string) {
		write_state_file()
		run_on_change_hook(ip, old_s, new_s)
	}(backend_ip(hc.lb), old_status, new_status)
}

func (hc *health_checker) run_check() bool {
	switch hc.cfg.ctype {
	case "tcp":
		conn, err := bound_dialer(hc.lb, hc.cfg.timeout).Dial("tcp4", hc.cfg.target)
		if err != nil {
			return false
		}
		conn.Close()
		return true

	case "http":
		client := &http.Client{
			Timeout: hc.cfg.timeout,
			Transport: &http.Transport{
				DialContext:       dial_context_via_backend(hc.lb, hc.cfg.timeout),
				DisableKeepAlives: true,
			},
		}
		resp, err := client.Get(hc.cfg.target)
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode >= 200 && resp.StatusCode < 400
	}
	return true
}
