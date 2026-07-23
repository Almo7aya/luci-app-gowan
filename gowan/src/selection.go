// selection.go
package main

import (
	"log"
	"math/big"
	"net"
	"strconv"
	"strings"
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
func pick_backend(client_ip, dest string, tried *big.Int) (*load_balancer, int) {
	if len(tried.Bits()) != 0 {
		return get_load_balancer(tried)
	}

	// dest is "host:port"; host may be an IP (transparent, or SOCKS IPv4)
	// or a domain (SOCKS with a hostname).
	dest_host, dest_port := split_dest(dest)
	if lb, i := policy_backend(client_ip, dest_host, dest_port); lb != nil {
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

func split_dest(dest string) (string, int) {
	host, portStr, err := net.SplitHostPort(dest)
	if err != nil {
		return "", 0
	}
	port, _ := strconv.Atoi(portStr)
	return host, port
}

func client_ip_of(conn net.Conn) string {
	host, _, err := net.SplitHostPort(conn.RemoteAddr().String())
	if err != nil {
		return ""
	}
	return host
}

// ---- policy routing --------------------------------------------------
//
// Rule types (evaluated in config order, first match wins):
//   client_ip  IP or CIDR of the connecting client
//   dest_ip    IP or CIDR of the destination (only matches when the
//              destination is an IP — i.e. transparent, or SOCKS-by-IP)
//   port       destination port; single, list, or lo:hi / lo-hi range
//   domain     destination hostname; only present for SOCKS clients that
//              send a name (transparent traffic carries only an IP)
// The Match field may be a comma-separated list for every type.

type port_range struct{ lo, hi int }

type policy_rule struct {
	ptype string
	wan   string
	nets  []*net.IPNet // client_ip / dest_ip
	ips   []net.IP     // client_ip / dest_ip
	ports []port_range // port
	doms  []string     // domain patterns, lowercased
}

var (
	policy_mu    sync.RWMutex
	policy_rules []policy_rule
)

func set_policies(list []policy_json) {
	rules := make([]policy_rule, 0, len(list))
	for _, p := range list {
		r := policy_rule{ptype: p.Type, wan: p.Wan}
		ok := false
		for _, tok := range strings.Split(p.Match, ",") {
			tok = strings.TrimSpace(tok)
			if tok == "" {
				continue
			}
			switch p.Type {
			case "client_ip", "dest_ip":
				if _, ipnet, err := net.ParseCIDR(tok); err == nil {
					r.nets = append(r.nets, ipnet)
					ok = true
				} else if ip := net.ParseIP(tok); ip != nil {
					r.ips = append(r.ips, ip)
					ok = true
				}
			case "port":
				if pr, valid := parse_port_range(tok); valid {
					r.ports = append(r.ports, pr)
					ok = true
				}
			case "domain":
				r.doms = append(r.doms, strings.ToLower(tok))
				ok = true
			}
		}
		if !ok {
			log.Println("[WARN] policy: no valid match in", p.Type, p.Match, "- skipped")
			continue
		}
		rules = append(rules, r)
	}

	policy_mu.Lock()
	policy_rules = rules
	policy_mu.Unlock()
	if len(rules) > 0 {
		log.Printf("[INFO] %d policy rule(s) active\n", len(rules))
	}
}

func parse_port_range(tok string) (port_range, bool) {
	sep := strings.IndexAny(tok, ":-")
	if sep < 0 {
		if n, err := strconv.Atoi(tok); err == nil && n > 0 && n < 65536 {
			return port_range{n, n}, true
		}
		return port_range{}, false
	}
	lo, e1 := strconv.Atoi(tok[:sep])
	hi, e2 := strconv.Atoi(tok[sep+1:])
	if e1 != nil || e2 != nil || lo < 1 || hi > 65535 || lo > hi {
		return port_range{}, false
	}
	return port_range{lo, hi}, true
}

func ip_in(ip net.IP, r *policy_rule) bool {
	for _, n := range r.nets {
		if n.Contains(ip) {
			return true
		}
	}
	for _, x := range r.ips {
		if x.Equal(ip) {
			return true
		}
	}
	return false
}

// domain_match: "*.example.com" and "example.com" both match example.com
// and any subdomain of it.
func domain_match(host string, doms []string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, p := range doms {
		base := strings.TrimPrefix(p, "*.")
		if host == base || strings.HasSuffix(host, "."+base) {
			return true
		}
	}
	return false
}

func (r *policy_rule) matches(client_ip, dest_ip net.IP, dest_host string, dest_port int) bool {
	switch r.ptype {
	case "client_ip":
		return client_ip != nil && ip_in(client_ip, r)
	case "dest_ip":
		return dest_ip != nil && ip_in(dest_ip, r)
	case "port":
		for _, pr := range r.ports {
			if dest_port >= pr.lo && dest_port <= pr.hi {
				return true
			}
		}
	case "domain":
		return dest_ip == nil && dest_host != "" && domain_match(dest_host, r.doms)
	}
	return false
}

// Returns the healthy backend a policy pins this connection to, or nil.
func policy_backend(client_ip_str, dest_host string, dest_port int) (*load_balancer, int) {
	client_ip := net.ParseIP(client_ip_str)
	dest_ip := net.ParseIP(dest_host) // nil when dest_host is a domain

	policy_mu.RLock()
	var wan string
	for i := range policy_rules {
		if policy_rules[i].matches(client_ip, dest_ip, dest_host, dest_port) {
			wan = policy_rules[i].wan
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
