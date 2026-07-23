// dispatch.go
package main

import (
	"context"
	"log"
	"math/big"
	"net"
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
Dials the requested address through a healthy backend, falling back to
the next backend when the outbound dial fails — one full pass over the
backend list before giving up. A live dial failure also feeds the
backend's health counters, so real outages converge faster than the
check interval. When socks is true the client is answered with SOCKS5
reply codes; transparent connections get no protocol bytes.
*/
func dispatch_connection(local_conn net.Conn, remote_address string, socks bool) {
	tried := new(big.Int)
	client_ip := client_ip_of(local_conn)

	for {
		lb, i := pick_backend(client_ip, tried)
		if lb == nil {
			log.Println("[WARN]", remote_address, "all load balancers failed")
			if socks {
				local_conn.Write([]byte{5, NETWORK_UNREACHABLE, 0, 1, 0, 0, 0, 0, 0, 0})
			}
			local_conn.Close()
			return
		}

		remote_conn, err := dial_out(lb, remote_address)
		if err != nil {
			log.Println("[WARN]", remote_address, "->", lb.address, "{"+err.Error()+"}", "LB:", i)
			lb.note_dial_failure()
			tried.SetBit(tried, i, 1)
			continue
		}

		debug_log(remote_address, "->", lb.address, "LB:", i)
		if socks {
			local_conn.Write([]byte{5, SUCCESS, 0, 1, 0, 0, 0, 0, 0, 0})
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
	dispatch_connection(local_conn, remote_address, true)
}
