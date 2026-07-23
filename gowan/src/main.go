// main.go
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

type load_balancer struct {
	name                string
	address             string
	iface               string
	contention_ratio    int
	current_connections int

	// Health + traffic state. Written by the backend's health checker,
	// the dispatch path and the startup/reload code; every access goes
	// through the global mutex.
	up            bool
	status_since  int64
	checks_ok     uint64
	checks_failed uint64
	total_conns   uint64
	active_conns  int64
}

// Bracket a proxied connection's lifetime for the status counters.
func (lb *load_balancer) conn_started() {
	mutex.Lock()
	lb.total_conns++
	lb.active_conns++
	mutex.Unlock()
}

func (lb *load_balancer) conn_finished() {
	mutex.Lock()
	lb.active_conns--
	mutex.Unlock()
}

// The load balancer used in the previous connection
var lb_index int = 0

// List of all load balancers
var lb_list []*load_balancer

// Mutex to serialize access to lb_list state (selection and health)
var mutex = &sync.Mutex{}

/*
Get a load balancer according to contention ratio.

Health-aware: backends marked DOWN are skipped — unless every backend is
DOWN, in which case all of them stay eligible (a proxy failing
per-connection beats a dead listener). The optional exclude set holds
backends the caller already tried this connection (dial fallback);
returns nil when every backend is excluded.
*/
func get_load_balancer(exclude *big.Int) (*load_balancer, int) {
	mutex.Lock()
	defer mutex.Unlock()

	all_down := true
	for _, lb := range lb_list {
		if lb.up {
			all_down = false
			break
		}
	}

	for tries := 0; tries < len(lb_list); tries++ {
		lb := lb_list[lb_index]
		i := lb_index

		excluded := exclude != nil && exclude.Bit(i) != 0
		if excluded || (!all_down && !lb.up) {
			lb.current_connections = 0
			advance_lb_index()
			continue
		}

		lb.current_connections++
		if lb.current_connections >= lb.contention_ratio {
			lb.current_connections = 0
			advance_lb_index()
		}
		return lb, i
	}
	return nil, -1
}

// Caller must hold mutex.
func advance_lb_index() {
	lb_index++
	if lb_index == len(lb_list) {
		lb_index = 0
	}
}

/*
Joins the local and remote connections together. done (optional) runs
once both directions have finished.
*/
func pipe_connections(local_conn, remote_conn net.Conn, done func()) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		defer remote_conn.Close()
		defer local_conn.Close()
		io.Copy(remote_conn, local_conn)
	}()

	go func() {
		defer wg.Done()
		defer remote_conn.Close()
		defer local_conn.Close()
		io.Copy(local_conn, remote_conn)
	}()

	if done != nil {
		go func() {
			wg.Wait()
			done()
		}()
	}
}

/*
Handle connections in tunnel mode
*/
func handle_tunnel_connection(conn net.Conn) {
	tried := new(big.Int)

	for {
		lb, i := get_load_balancer(tried)
		if lb == nil {
			log.Println("[WARN] all load balancers failed")
			conn.Close()
			return
		}

		remote_addr, _ := net.ResolveTCPAddr("tcp4", lb.address)
		remote_conn, err := net.DialTCP("tcp4", nil, remote_addr)
		if err != nil {
			log.Println("[WARN]", lb.address, fmt.Sprintf("{%s}", err), "LB:", i)
			lb.note_dial_failure()
			tried.SetBit(tried, i, 1)
			continue
		}

		debug_log("Tunnelled to", lb.address, "LB:", i)
		lb.conn_started()
		pipe_connections(conn, remote_conn, lb.conn_finished)
		return
	}
}

/*
Calls the apprpriate handle_connections based on tunnel mode
*/
func handle_connection(conn net.Conn, tunnel bool) {
	if tunnel {
		handle_tunnel_connection(conn)
	} else if address, err := handle_socks_connection(conn); err == nil {
		server_response(conn, address)
	}
}

/*
Detect the addresses which can  be used for dispatching in non-tunnelling mode.
Alternate to ipconfig/ifconfig
*/
func detect_interfaces() {
	fmt.Println("--- Listing the available adresses for dispatching")
	ifaces, _ := net.Interfaces()

	for _, iface := range ifaces {
		if (iface.Flags&net.FlagUp == net.FlagUp) && (iface.Flags&net.FlagLoopback != net.FlagLoopback) {
			addrs, _ := iface.Addrs()
			for _, addr := range addrs {
				if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
					if ipnet.IP.To4() != nil {
						fmt.Printf("[+] %s, IPv4:%s\n", iface.Name, ipnet.IP.String())
					}
				}
			}
		}
	}

}

/*
Gets the interface associated with the IP
*/
func get_iface_from_ip(ip string) string {
	ifaces, _ := net.Interfaces()

	for _, iface := range ifaces {
		if (iface.Flags&net.FlagUp == net.FlagUp) && (iface.Flags&net.FlagLoopback != net.FlagLoopback) {
			addrs, _ := iface.Addrs()
			for _, addr := range addrs {
				if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
					if ipnet.IP.To4() != nil {
						if ipnet.IP.String() == ip {
							return iface.Name
						}
					}
				}
			}
		}
	}
	return ""
}

/*
Parses the command line arguements to obtain the list of load balancers
*/
func parse_load_balancers(args []string, tunnel bool) {
	if len(args) == 0 {
		log.Fatal("[FATAL] Please specify one or more load balancers")
	}

	lb_list = make([]*load_balancer, len(args))

	for idx, a := range args {
		splitted := strings.Split(a, "@")
		iface := ""
		// IP address of a Fully Qualified Domain Name of the load balancer
		var lb_ip_or_fqdn string
		var lb_port int
		var err error

		if tunnel {
			ip_or_fqdn_port := strings.Split(splitted[0], ":")
			if len(ip_or_fqdn_port) != 2 {
				log.Fatal("[FATAL] Invalid address specification ", splitted[0])
				return
			}

			lb_ip_or_fqdn = ip_or_fqdn_port[0]
			lb_port, err = strconv.Atoi(ip_or_fqdn_port[1])
			if err != nil || lb_port <= 0 || lb_port > 65535 {
				log.Fatal("[FATAL] Invalid port ", splitted[0])
				return
			}

		} else {
			lb_ip_or_fqdn = splitted[0]
			lb_port = 0
		}

		// FQDN not supported for tunnel modes
		if !tunnel && net.ParseIP(lb_ip_or_fqdn).To4() == nil {
			log.Fatal("[FATAL] Invalid address ", lb_ip_or_fqdn)
		}

		var cont_ratio int = 1
		if len(splitted) > 1 {
			cont_ratio, err = strconv.Atoi(splitted[1])
			if err != nil || cont_ratio <= 0 {
				log.Fatal("[FATAL] Invalid contention ratio for ", lb_ip_or_fqdn)
			}
		}

		// Obtaining the interface name of the load balancer IP's doesn't make sense in tunnel mode
		if !tunnel {
			iface = get_iface_from_ip(lb_ip_or_fqdn)
			if iface == "" {
				log.Fatal("[FATAL] IP address not associated with an interface ", lb_ip_or_fqdn)
			}
		}

		slbport := ""
		if tunnel {
			slbport = ":" + strconv.Itoa(lb_port)
		}

		log.Printf("[INFO] Load balancer %d: %s%s, contention ratio: %d\n", idx+1, lb_ip_or_fqdn, slbport, cont_ratio)
		lb_list[idx] = &load_balancer{
			name:             lb_ip_or_fqdn,
			address:          fmt.Sprintf("%s:%d", lb_ip_or_fqdn, lb_port),
			iface:            iface,
			contention_ratio: cont_ratio,
			up:               true,
			status_since:     time.Now().Unix(),
		}
	}
}

/*
Main function
*/
func main() {
	var lhost = flag.String("lhost", "127.0.0.1", "The host to listen for SOCKS connection")
	var lport = flag.Int("lport", 8080, "The local port to listen for SOCKS connection")
	var detect = flag.Bool("list", false, "Shows the available addresses for dispatching (non-tunnelling mode only)")
	var tunnel = flag.Bool("tunnel", false, "Use tunnelling mode (acts as a transparent load balancing proxy)")
	var debug = flag.Bool("debug", false, "Log every dispatched connection/flow ([DEBUG] lines); off by default")

	var check_type = flag.String("check-type", "none", "Health check type: tcp, http or none")
	var check_target = flag.String("check-target", "8.8.8.8:53", "Health check target (host:port for tcp, URL for http)")
	var check_interval = flag.Int("check-interval", 30, "Seconds between health checks per backend")
	var check_timeout = flag.Int("check-timeout", 5, "Health check timeout in seconds")
	var check_fail = flag.Int("check-fail", 3, "Consecutive failures before a backend is marked DOWN")
	var check_rise = flag.Int("check-rise", 2, "Consecutive successes before a backend is marked UP")
	var state_path = flag.String("state-file", "", "Write backend health state as JSON to this file")
	var on_change = flag.String("on-change", "", "Run '<cmd> <backend-ip> <old-state> <new-state>' on every health flip")
	var transparent_port = flag.Int("transparent", 0, "Also accept nft/iptables-REDIRECTed connections on this port (Linux only, 0 = off)")
	var transparent_udp_port = flag.Int("transparent-udp", 0, "Relay TPROXY'd UDP on this port across the WANs (Linux only, 0 = off)")
	var udp_timeout = flag.Int("udp-timeout", 60, "Idle seconds before a UDP flow is dropped")
	var backends_path = flag.String("backends-file", "", "Load backends (with per-backend check config) from this JSON file; SIGHUP re-reads it without dropping connections")
	var policy_path = flag.String("policy-file", "", "Load client-IP policy rules from this JSON file (SIGHUP re-reads it)")
	var api_addr = flag.String("api", "", "Serve GET /status as JSON on this address, e.g. 127.0.0.1:9080 (empty = off)")
	var sticky = flag.Bool("sticky", false, "Pin each client IP to the same backend for the session")
	var sticky_timeout = flag.Int("sticky-timeout", 300, "Sticky mapping lifetime in seconds")
	var auth_u = flag.String("auth-user", "", "SOCKS5 username (enables RFC 1929 auth on the SOCKS listener)")
	var auth_p = flag.String("auth-pass", "", "SOCKS5 password")

	flag.Parse()
	if *detect {
		detect_interfaces()
		return
	}

	// Disable timestamp in log messages
	log.SetFlags(log.Flags() &^ (log.Ldate | log.Ltime))

	// Check for valid IP
	if net.ParseIP(*lhost).To4() == nil {
		log.Fatal("[FATAL] Invalid host ", *lhost)
	}

	// Check for valid port
	if *lport < 1 || *lport > 65535 {
		log.Fatal("[FATAL] Invalid port ", *lport)
	}

	if *transparent_port < 0 || *transparent_port > 65535 || *transparent_port == *lport {
		log.Fatal("[FATAL] Invalid transparent port ", *transparent_port)
	}
	if *transparent_port != 0 && *tunnel {
		log.Fatal("[FATAL] transparent mode is not available in tunnel mode")
	}
	if *transparent_udp_port < 0 || *transparent_udp_port > 65535 {
		log.Fatal("[FATAL] Invalid transparent-udp port ", *transparent_udp_port)
	}
	if *transparent_udp_port != 0 && *tunnel {
		log.Fatal("[FATAL] transparent UDP is not available in tunnel mode")
	}

	switch *check_type {
	case "none", "tcp", "http":
	default:
		log.Fatal("[FATAL] Invalid check type ", *check_type)
	}
	if *backends_path != "" && *tunnel {
		log.Fatal("[FATAL] -backends-file is not available in tunnel mode")
	}

	debug_enabled = *debug
	state_file = *state_path
	on_change_cmd = *on_change
	global_check_cfg = check_config{
		ctype:    *check_type,
		target:   *check_target,
		interval: time.Duration(*check_interval) * time.Second,
		timeout:  time.Duration(*check_timeout) * time.Second,
		fail:     *check_fail,
		rise:     *check_rise,
	}
	if *tunnel && *check_type != "none" {
		log.Println("[WARN] health checks are not supported in tunnel mode, disabled")
		global_check_cfg.ctype = "none"
	}

	if *backends_path != "" {
		// Backends (and per-backend check config) come from the file;
		// SIGHUP re-reads it. Positional arguments are ignored.
		backends_file = *backends_path
		list, err := load_backends_file(backends_file)
		if err != nil {
			log.Fatalln("[FATAL]", err)
		}
		if apply_backends(list) == 0 {
			log.Fatalln("[FATAL] backends file has no usable backends")
		}
	} else {
		//Parse remaining string to get addresses of load balancers
		parse_load_balancers(flag.Args(), *tunnel)

		cfgs := make([]check_config, len(lb_list))
		for i := range cfgs {
			cfgs[i] = global_check_cfg
		}
		start_health_checks(cfgs)
	}

	auth_user = *auth_u
	auth_pass = *auth_p
	if auth_enabled() {
		log.Println("[INFO] SOCKS5 username/password authentication enabled")
	}
	configure_sticky(*sticky, time.Duration(*sticky_timeout)*time.Second)
	if *sticky {
		log.Printf("[INFO] Sticky sessions enabled (timeout %ds)\n", *sticky_timeout)
	}

	if *policy_path != "" {
		policy_file = *policy_path
		reload_policies()
	}

	write_state_file()
	setup_reload_handler()

	if *api_addr != "" {
		start_status_api(*api_addr)
	}

	if *transparent_port != 0 {
		start_transparent_listener(*lhost, *transparent_port)
	}

	if *transparent_udp_port != 0 {
		start_udp_transparent(*lhost, *transparent_udp_port, time.Duration(*udp_timeout)*time.Second)
	}

	local_bind_address := fmt.Sprintf("%s:%d", *lhost, *lport)

	// Start local server
	l, err := net.Listen("tcp4", local_bind_address)
	if err != nil {
		log.Fatalln("[FATAL] Could not start local server on ", local_bind_address)
	}
	log.Println("[INFO] Local server started on ", local_bind_address)
	defer l.Close()

	for {
		conn, err := l.Accept()
		if err != nil {
			log.Println("[WARN] Could not accept connection")
		} else {
			go handle_connection(conn, *tunnel)
		}
	}
}
