// selection.go
package main

import (
	"log"
	"math/big"
	"net"
	"sync"
	"time"
)

/*
Backend selection layers policy and sticky routing on top of the
weighted round-robin. Only the FIRST attempt for a connection consults
policy/sticky; dial-failure fallback (tried != empty) always uses plain
round-robin so a broken pinned backend can still fail over.

Order on the first attempt:
 1. policy rule matching the client IP -> that backend (if healthy)
 2. existing sticky mapping for the client IP  -> same backend (if healthy)
 3. weighted round-robin, and remember the choice for stickiness
*/
func pick_backend(client_ip string, tried *big.Int) (*load_balancer, int) {
	if len(tried.Bits()) != 0 {
		return get_load_balancer(tried)
	}

	if lb, i := policy_backend(client_ip); lb != nil {
		return lb, i
	}
	if lb, i := sticky_lookup(client_ip); lb != nil {
		return lb, i
	}

	lb, i := get_load_balancer(tried)
	if lb != nil {
		sticky_remember(client_ip, i)
	}
	return lb, i
}

func client_ip_of(conn net.Conn) string {
	host, _, err := net.SplitHostPort(conn.RemoteAddr().String())
	if err != nil {
		return ""
	}
	return host
}

// ---- policy routing --------------------------------------------------

type policy_rule struct {
	ptype string // only "client_ip" is enforced today
	ip    net.IP
	ipnet *net.IPNet
	wan   string // backend name
}

var (
	policy_mu    sync.RWMutex
	policy_rules []policy_rule
)

func set_policies(list []policy_json) {
	rules := make([]policy_rule, 0, len(list))
	for _, p := range list {
		if p.Type != "client_ip" {
			continue // domain/port/dest_ip not enforced yet
		}
		r := policy_rule{ptype: p.Type, wan: p.Wan}
		if _, ipnet, err := net.ParseCIDR(p.Match); err == nil {
			r.ipnet = ipnet
		} else if ip := net.ParseIP(p.Match); ip != nil {
			r.ip = ip
		} else {
			log.Println("[WARN] policy: invalid match", p.Match, "- skipped")
			continue
		}
		rules = append(rules, r)
	}

	policy_mu.Lock()
	policy_rules = rules
	policy_mu.Unlock()
	if len(rules) > 0 {
		log.Printf("[INFO] %d client-IP policy rule(s) active\n", len(rules))
	}
}

// Returns the healthy backend a policy pins this client to, or nil.
func policy_backend(client_ip string) (*load_balancer, int) {
	if client_ip == "" {
		return nil, -1
	}
	ip := net.ParseIP(client_ip)
	if ip == nil {
		return nil, -1
	}

	policy_mu.RLock()
	var wan string
	for _, r := range policy_rules {
		if (r.ipnet != nil && r.ipnet.Contains(ip)) || (r.ip != nil && r.ip.Equal(ip)) {
			wan = r.wan
			break
		}
	}
	policy_mu.RUnlock()
	if wan == "" {
		return nil, -1
	}

	mutex.Lock()
	defer mutex.Unlock()
	for i, lb := range lb_list {
		if lb.name == wan && lb.up {
			return lb, i
		}
	}
	return nil, -1
}

// ---- sticky sessions -------------------------------------------------

type sticky_entry struct {
	name    string
	expires time.Time
}

var (
	sticky_enabled bool
	sticky_ttl     time.Duration
	sticky_mu      sync.Mutex
	sticky_map     = map[string]sticky_entry{}
)

func configure_sticky(enabled bool, ttl time.Duration) {
	sticky_mu.Lock()
	sticky_enabled = enabled
	sticky_ttl = ttl
	if !enabled {
		sticky_map = map[string]sticky_entry{}
	}
	sticky_mu.Unlock()
}

// Returns the healthy backend this client is currently pinned to, if the
// mapping exists and has not expired.
func sticky_lookup(client_ip string) (*load_balancer, int) {
	if client_ip == "" {
		return nil, -1
	}
	sticky_mu.Lock()
	if !sticky_enabled {
		sticky_mu.Unlock()
		return nil, -1
	}
	e, ok := sticky_map[client_ip]
	if ok && time.Now().After(e.expires) {
		delete(sticky_map, client_ip)
		ok = false
	}
	sticky_mu.Unlock()
	if !ok {
		return nil, -1
	}

	mutex.Lock()
	defer mutex.Unlock()
	for i, lb := range lb_list {
		if lb.name == e.name && lb.up {
			// Refresh TTL on use.
			sticky_mu.Lock()
			sticky_map[client_ip] = sticky_entry{name: e.name, expires: time.Now().Add(sticky_ttl)}
			sticky_mu.Unlock()
			return lb, i
		}
	}
	return nil, -1
}

func sticky_remember(client_ip string, idx int) {
	if client_ip == "" {
		return
	}
	sticky_mu.Lock()
	defer sticky_mu.Unlock()
	if !sticky_enabled {
		return
	}
	mutex.Lock()
	name := ""
	if idx >= 0 && idx < len(lb_list) {
		name = lb_list[idx].name
	}
	mutex.Unlock()
	if name != "" {
		sticky_map[client_ip] = sticky_entry{name: name, expires: time.Now().Add(sticky_ttl)}
	}
}
