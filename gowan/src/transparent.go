//go:build linux

// transparent.go
package main

import (
	"fmt"
	"log"
	"net"
	"syscall"
)

// SO_ORIGINAL_DST retrieves the pre-DNAT destination recorded by
// conntrack for connections that hit an nftables/iptables REDIRECT rule.
const SO_ORIGINAL_DST = 80

/*
Decodes the sockaddr_in returned by SO_ORIGINAL_DST (as the Multiaddr
bytes of syscall.IPv6Mreq — the classic Go idiom for this getsockopt):
bytes 2-3 hold the port (network order), bytes 4-7 the IPv4 address.
*/
func orig_dst_from_bytes(m [16]byte) string {
	port := int(m[2])<<8 | int(m[3])
	return fmt.Sprintf("%d.%d.%d.%d:%d", m[4], m[5], m[6], m[7], port)
}

func get_original_dst(conn *net.TCPConn) (string, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return "", err
	}

	var dst string
	var sockopt_err error
	err = raw.Control(func(fd uintptr) {
		mreq, e := syscall.GetsockoptIPv6Mreq(int(fd), syscall.SOL_IP, SO_ORIGINAL_DST)
		if e != nil {
			sockopt_err = e
			return
		}
		dst = orig_dst_from_bytes(mreq.Multiaddr)
	})
	if err != nil {
		return "", err
	}
	if sockopt_err != nil {
		return "", sockopt_err
	}
	return dst, nil
}

/*
Accepts nft/iptables-REDIRECTed connections and dispatches them to the
recorded original destination — no SOCKS handshake, zero client
configuration. Connections whose original destination IS the listener
itself (someone connected to the port directly, no REDIRECT involved)
are dropped: dialing them would loop the proxy into itself.
*/
func start_transparent_listener(lhost string, lport int) {
	bind_address := fmt.Sprintf("%s:%d", lhost, lport)

	l, err := net.Listen("tcp4", bind_address)
	if err != nil {
		log.Fatalln("[FATAL] Could not start transparent listener on", bind_address)
	}
	log.Println("[INFO] Transparent listener started on", bind_address)

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				log.Println("[WARN] Could not accept transparent connection")
				continue
			}
			go handle_transparent_connection(conn, lport)
		}
	}()
}

func handle_transparent_connection(conn net.Conn, lport int) {
	tcp_conn, ok := conn.(*net.TCPConn)
	if !ok {
		conn.Close()
		return
	}

	dst, err := get_original_dst(tcp_conn)
	if err != nil {
		log.Println("[WARN] transparent: no original destination:", err)
		conn.Close()
		return
	}

	// Direct (non-redirected) connections report the listener's own
	// address as "original destination" — never dial those.
	if _, port, err := net.SplitHostPort(dst); err == nil && port == fmt.Sprint(lport) {
		log.Println("[WARN] transparent: dropping direct connection from", conn.RemoteAddr())
		conn.Close()
		return
	}

	dispatch_connection(conn, dst, false)
}
