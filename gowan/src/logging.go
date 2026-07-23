// logging.go
package main

import "log"

// When false (production default), per-connection/per-flow [DEBUG] lines
// are suppressed; [INFO], [WARN] and [FATAL] always log. Set via -debug.
var debug_enabled bool

func debug_log(v ...interface{}) {
	if debug_enabled {
		log.Println(append([]interface{}{"[DEBUG]"}, v...)...)
	}
}

func debug_logf(format string, v ...interface{}) {
	if debug_enabled {
		log.Printf("[DEBUG] "+format, v...)
	}
}
