//go:build linux

// dial_linux.go
package main

import (
	"log"
	"net"
	"syscall"
	"time"
)

/*
Returns a dialer bound to the backend's source address and, via
SO_BINDTODEVICE, its interface — egress is forced out that WAN
regardless of routing tables.

NOTE: Requires root or cap_net_raw (procd runs the daemon as root on
OpenWrt; elsewhere: sudo setcap cap_net_raw=eip ./gowan).
*/
func bound_dialer(lb *load_balancer, timeout time.Duration) *net.Dialer {
	local_tcpaddr, _ := net.ResolveTCPAddr("tcp4", lb.address)
	return &net.Dialer{
		Timeout:   timeout,
		LocalAddr: local_tcpaddr,
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				if err := syscall.BindToDevice(int(fd), lb.iface); err != nil {
					log.Println("[WARN] Couldn't bind to interface", lb.iface)
				}
			})
		},
	}
}
