// reload.go
package main

import (
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Path of the backends file (-backends-file); empty = flags-only mode.
var backends_file string

// Path of the policies file (-policy-file); empty = no policy routing.
var policy_file string

// Re-reads the policies file if configured. Missing file = clear rules.
func reload_policies() {
	if policy_file == "" {
		return
	}
	list, err := load_policies_file(policy_file)
	if err != nil {
		log.Println("[WARN] policy reload failed, keeping current rules:", err)
		return
	}
	set_policies(list)
}

func setup_reload_handler() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGHUP)
	go func() {
		for range c {
			reload_backends()
		}
	}()
}

// Flush usage totals to the persistent file on SIGTERM/SIGINT (procd
// stop) so a graceful stop doesn't lose accounting since the last flush.
func setup_shutdown_flush() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-c
		flush_usage()
		os.Exit(0)
	}()
}

/*
SIGHUP: re-read the backends file and swap the backend set without
touching the listeners — active connections are never dropped.
*/
func reload_backends() {
	if backends_file == "" {
		log.Println("[WARN] SIGHUP ignored: started without -backends-file")
		return
	}

	list, err := load_backends_file(backends_file)
	if err != nil {
		log.Println("[WARN] reload failed, keeping current backends:", err)
		return
	}

	n := apply_backends(list)
	if n > 0 {
		reload_policies()
		log.Printf("[INFO] reloaded: %d backend(s) active\n", n)
		write_state_file()
	}
}

/*
Swaps lb_list to match the given configs. Backends whose IP survives
the reload REUSE their existing struct: health verdict, check counters
and connection counters carry over, and connections opened before the
reload keep decrementing the right active-counter when they close.
Returns the number of active backends, or 0 if nothing in the list was
usable (in which case the current set is kept).
*/
func apply_backends(list []backend_json) int {
	type candidate struct {
		bj    backend_json
		iface string
	}

	var cands []candidate
	for _, b := range list {
		if net.ParseIP(b.IP).To4() == nil {
			log.Println("[WARN] backends file: invalid IPv4", b.IP, "- skipped")
			continue
		}
		iface := get_iface_from_ip(b.IP)
		if iface == "" {
			log.Println("[WARN] backends file:", b.IP, "not on any interface - skipped")
			continue
		}
		cands = append(cands, candidate{bj: b, iface: iface})
	}
	if len(cands) == 0 {
		log.Println("[WARN] backends file has no usable backends, keeping current set")
		return 0
	}

	stop_health_checks()

	mutex.Lock()
	old := make(map[string]*load_balancer, len(lb_list))
	for _, lb := range lb_list {
		old[backend_ip(lb)] = lb
	}

	newlist := make([]*load_balancer, 0, len(cands))
	newcfgs := make([]check_config, 0, len(cands))
	for _, c := range cands {
		lb, exists := old[c.bj.IP]
		if exists {
			lb.name = c.bj.Name
			lb.iface = c.iface
			lb.contention_ratio = c.bj.Ratio
			lb.metric = c.bj.Metric
			lb.current_connections = 0
		} else {
			lb = &load_balancer{
				name:             c.bj.Name,
				address:          c.bj.IP + ":0",
				iface:            c.iface,
				contention_ratio: c.bj.Ratio,
				metric:           c.bj.Metric,
				up:               true,
				status_since:     time.Now().Unix(),
			}
		}
		if lb.name == "" {
			lb.name = c.bj.IP
		}
		newlist = append(newlist, lb)
		newcfgs = append(newcfgs, merged_check_config(c.bj))
		log.Printf("[INFO] backend %s: %s@%d via %s\n", lb.name, c.bj.IP, c.bj.Ratio, c.iface)
	}
	lb_list = newlist
	lb_index = 0
	mutex.Unlock()

	start_health_checks(newcfgs)
	return len(newlist)
}
