// auth.go
package main

import "crypto/subtle"

// SOCKS5 username/password (RFC 1929). Empty user = auth disabled.
var auth_user string
var auth_pass string

func auth_enabled() bool {
	return auth_user != ""
}

// Constant-time comparison so validation time doesn't leak the secret.
func credentials_ok(user, pass string) bool {
	u := subtle.ConstantTimeCompare([]byte(user), []byte(auth_user))
	p := subtle.ConstantTimeCompare([]byte(pass), []byte(auth_pass))
	return u == 1 && p == 1
}
