// dispatch.go
package main

import (
	"context"
	"log"
	"math/big"
	"net"
	"strconv"
	"time"
)

// dial_out is a package variable so tests can substitute a fake dialer.
var dial_out = dial_via_backend

func dial_via_backend(lb *load_balancer, remote_address string) (net.Conn, error) {
	return bound_dialer(lb, 0).Dial("tcp4", remote_address)
}

// Context-aware variant used by the health checker's HTTP client.
func dial_context_via_backend(lb *load_balancer, timeout time.Duration) func(context.Context, string, string) (net.Conn, error) {
	d := bound_dialer(lb, timeout)
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		return d.DialContext(ctx, "tcp4", address)
	}
}

/*
dispatch dials dial_addr through a healthy backend, falling back to the
next backend when the outbound dial fails — one full pass over the
backend list before giving up. A live dial failure also feeds the
backend's health counters. When socks is true the client is answered
with SOCKS5 reply codes; transparent connections get no protocol bytes.

policy_host, when set, is the destination hostname used for domain
policy matching (SNI / Host from a transparent connection); when empty
it is derived from dial_addr if that is itself a hostname (SOCKS).
prebuf holds bytes already read from the client (during sniffing) that
must be replayed to the backend before piping.
*/
func dispatch(local_conn net.Conn, dial_addr, policy_host string, socks bool, prebuf []byte) {
	tried := new(big.Int)
	client_ip := client_ip_of(local_conn)

	host, ps, err := net.SplitHostPort(dial_addr)
	port := 0
	if err == nil {
		port, _ = strconv.Atoi(ps)
	}
	dest_ip, dest_host := "", policy_host
	if net.ParseIP(host) != nil {
		dest_ip = host // dialing an IP directly
	} else if dest_host == "" {
		dest_host = host // SOCKS hostname
	}

	for {
		lb, i := pick_backend(client_ip, dest_ip, dest_host, port, tried)
		if lb == nil {
			log.Println("[WARN]", dial_addr, "all load balancers failed")
			if socks {
				local_conn.Write([]byte{5, NETWORK_UNREACHABLE, 0, 1, 0, 0, 0, 0, 0, 0})
			}
			local_conn.Close()
			return
		}

		remote_conn, err := dial_out(lb, dial_addr)
		if err != nil {
			log.Println("[WARN]", dial_addr, "->", lb.address, "{"+err.Error()+"}", "LB:", i)
			lb.note_dial_failure()
			tried.SetBit(tried, i, 1)
			continue
		}

		debug_log(dial_addr, "->", lb.address, "LB:", i)
		if socks {
			local_conn.Write([]byte{5, SUCCESS, 0, 1, 0, 0, 0, 0, 0, 0})
		}
		if len(prebuf) > 0 {
			remote_conn.Write(prebuf) // replay the sniffed client bytes
		}
		lb.conn_started()
		pipe_connections(local_conn, remote_conn, lb.conn_finished)
		return
	}
}

/*
Implements the server response of SOCKS5.
*/
func server_response(local_conn net.Conn, remote_address string) {
	dispatch(local_conn, remote_address, "", true, nil)
}
