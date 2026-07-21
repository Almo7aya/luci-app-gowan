//go:build !linux

// transparent_other.go
package main

import "log"

/*
Transparent interception needs SO_ORIGINAL_DST and nftables — both
Linux-only. The flag is refused elsewhere so this can only be reached
by a build for a platform GoWAN does not target.
*/
func start_transparent_listener(lhost string, lport int) {
	log.Fatalln("[FATAL] transparent mode requires Linux")
}
