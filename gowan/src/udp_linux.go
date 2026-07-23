//go:build linux

// udp_linux.go
package main

import (
	"fmt"
	"log"
	"math/big"
	"net"
	"syscall"
	"time"
)

// setsockopt constants not exposed by the syscall package.
const (
	_IP_TRANSPARENT     = 19
	_IP_RECVORIGDSTADDR = 20
	_IP_ORIGDSTADDR     = 20 // cmsg type, same value
	_SO_REUSEPORT       = 15 // not exposed by stdlib syscall on Linux
)

var udp_flows *udp_table

/*
Starts the transparent UDP relay: a TPROXY'd listener that reads each
datagram's real destination and relays it out a selected WAN, returning
replies to the client with the original destination as source.
*/
func start_udp_transparent(lhost string, lport int, timeout time.Duration) {
	udp_flows = new_udp_table(timeout)

	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, 0)
	if err != nil {
		log.Fatalln("[FATAL] udp: socket:", err)
	}
	if err := syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); err != nil {
		log.Fatalln("[FATAL] udp: SO_REUSEADDR:", err)
	}
	// TPROXY delivery + original-destination retrieval.
	if err := syscall.SetsockoptInt(fd, syscall.IPPROTO_IP, _IP_TRANSPARENT, 1); err != nil {
		log.Fatalln("[FATAL] udp: IP_TRANSPARENT (need root):", err)
	}
	if err := syscall.SetsockoptInt(fd, syscall.IPPROTO_IP, _IP_RECVORIGDSTADDR, 1); err != nil {
		log.Fatalln("[FATAL] udp: IP_RECVORIGDSTADDR:", err)
	}

	sa := &syscall.SockaddrInet4{Port: lport}
	copy(sa.Addr[:], net.ParseIP(lhost).To4())
	if err := syscall.Bind(fd, sa); err != nil {
		log.Fatalln("[FATAL] udp: bind:", err)
	}

	go udp_flows.reap_loop()
	go udp_listen_loop(fd)
	log.Printf("[INFO] Transparent UDP relay started on %s:%d\n", lhost, lport)
}

func udp_listen_loop(fd int) {
	buf := make([]byte, 65535)
	oob := make([]byte, 1024)
	for {
		n, oobn, _, from, err := syscall.Recvmsg(fd, buf, oob, 0)
		if err != nil {
			log.Println("[WARN] udp: recvmsg:", err)
			continue
		}
		client, ok := from.(*syscall.SockaddrInet4)
		if !ok {
			continue
		}
		dst, ok := parse_origdst(oob[:oobn])
		if !ok {
			continue
		}
		payload := make([]byte, n)
		copy(payload, buf[:n])
		udp_handle_packet(client, dst, payload)
	}
}

// Extracts the original destination from the recvmsg control messages.
func parse_origdst(oob []byte) (*syscall.SockaddrInet4, bool) {
	cmsgs, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		return false_sa()
	}
	for _, c := range cmsgs {
		if c.Header.Level == syscall.IPPROTO_IP && c.Header.Type == _IP_ORIGDSTADDR {
			if len(c.Data) < 8 {
				return false_sa()
			}
			sa := &syscall.SockaddrInet4{}
			// sockaddr_in: family(2) port(2, BE) addr(4)
			sa.Port = int(c.Data[2])<<8 | int(c.Data[3])
			copy(sa.Addr[:], c.Data[4:8])
			return sa, true
		}
	}
	return false_sa()
}

func false_sa() (*syscall.SockaddrInet4, bool) { return nil, false }

func sa_string(sa *syscall.SockaddrInet4) string {
	return fmt.Sprintf("%d.%d.%d.%d:%d", sa.Addr[0], sa.Addr[1], sa.Addr[2], sa.Addr[3], sa.Port)
}

func sa_ip(sa *syscall.SockaddrInet4) string {
	return fmt.Sprintf("%d.%d.%d.%d", sa.Addr[0], sa.Addr[1], sa.Addr[2], sa.Addr[3])
}

func udp_handle_packet(client, dst *syscall.SockaddrInet4, payload []byte) {
	key := sa_string(client) + "->" + sa_string(dst)

	flow, created := udp_flows.get_or_create(key, func() *udp_flow {
		return create_udp_flow(client, dst)
	})
	if flow == nil {
		return
	}
	if created {
		log.Printf("[DEBUG] udp %s -> %s via %s\n", sa_string(client), sa_string(dst), flow.lb.iface)
	}

	// Send the client's datagram out the selected WAN.
	if ec := flow_egress(flow); ec != nil {
		ec.Write(payload)
	}
	udp_flows.touch(key)
}

// Per-flow socket bundle, stored on udp_flow.plat (set once at creation,
// read-only thereafter — no shared-map race).
type udp_sockets struct {
	egress  *net.UDPConn
	replyFd int
}

func flow_egress(f *udp_flow) *net.UDPConn {
	if s, ok := f.plat.(*udp_sockets); ok {
		return s.egress
	}
	return nil
}

func create_udp_flow(client, dst *syscall.SockaddrInet4) *udp_flow {
	clientIP := sa_ip(client)

	lb, _ := pick_backend(clientIP, new(big.Int))
	if lb == nil {
		return nil
	}

	// Egress socket bound to the chosen WAN (source IP + SO_BINDTODEVICE),
	// connected to the real destination.
	egress, err := udp_egress_conn(lb, sa_string(dst))
	if err != nil {
		log.Println("[WARN] udp: egress dial:", err)
		return nil
	}

	// Reply socket: IP_TRANSPARENT, bound to the original destination so
	// datagrams sent to the client carry dst as their source.
	replyFd, err := transparent_reply_socket(dst)
	if err != nil {
		log.Println("[WARN] udp: reply socket:", err)
		egress.Close()
		return nil
	}

	lb.conn_started()
	f := &udp_flow{lb: lb, plat: &udp_sockets{egress: egress, replyFd: replyFd}}

	clientSa := *client // copy for the closure
	f.closefn = func() {
		egress.Close()
		syscall.Close(replyFd)
		lb.conn_finished()
	}

	// Reply pump: real server -> client (spoofed source = dst).
	go func() {
		rbuf := make([]byte, 65535)
		for {
			egress.SetReadDeadline(time.Now().Add(udp_flows.timeout))
			n, err := egress.Read(rbuf)
			if err != nil {
				return // deadline or closed: reaper handles teardown
			}
			syscall.Sendto(replyFd, rbuf[:n], 0, &clientSa)
		}
	}()

	return f
}

// UDP socket bound to a backend's interface + source IP, connected to dst.
func udp_egress_conn(lb *load_balancer, dst string) (*net.UDPConn, error) {
	d := &net.Dialer{
		LocalAddr: &net.UDPAddr{IP: net.ParseIP(backend_ip(lb))},
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				if err := syscall.BindToDevice(int(fd), lb.iface); err != nil {
					log.Println("[WARN] udp: BindToDevice", lb.iface, err)
				}
			})
		},
	}
	conn, err := d.Dial("udp4", dst)
	if err != nil {
		return nil, err
	}
	return conn.(*net.UDPConn), nil
}

// Raw UDP socket with IP_TRANSPARENT, bound to a (possibly non-local)
// address so replies to the client use it as the source.
func transparent_reply_socket(src *syscall.SockaddrInet4) (int, error) {
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, 0)
	if err != nil {
		return -1, err
	}
	if err := syscall.SetsockoptInt(fd, syscall.IPPROTO_IP, _IP_TRANSPARENT, 1); err != nil {
		syscall.Close(fd)
		return -1, err
	}
	// Two flows can share the same original destination; SO_REUSEPORT lets
	// several sockets bind the identical address.
	syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
	syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, _SO_REUSEPORT, 1)
	if err := syscall.Bind(fd, src); err != nil {
		syscall.Close(fd)
		return -1, err
	}
	return fd, nil
}
