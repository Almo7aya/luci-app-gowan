//go:build !linux

// udp_other.go
package main

import (
	"log"
	"time"
)

// Transparent UDP relaying needs TPROXY + IP_TRANSPARENT, both Linux-only.
func start_udp_transparent(lhost string, lport int, timeout time.Duration) {
	log.Fatalln("[FATAL] transparent UDP requires Linux")
}
