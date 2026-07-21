//go:build !linux

// dial_other.go
package main

import (
	"net"
	"time"
)

/*
Non-Linux fallback: binds the source address only. Interface binding
(SO_BINDTODEVICE) is Linux-specific; on other systems the OS routing
table decides the egress interface for the bound source address.
*/
func bound_dialer(lb *load_balancer, timeout time.Duration) *net.Dialer {
	local_tcpaddr, _ := net.ResolveTCPAddr("tcp4", lb.address)
	return &net.Dialer{
		Timeout:   timeout,
		LocalAddr: local_tcpaddr,
	}
}
