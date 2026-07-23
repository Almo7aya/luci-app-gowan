// sniff.go
//
// Extracts the destination hostname from the first client bytes so
// domain policy rules can apply to transparent connections (which carry
// only an IP at L3): TLS SNI on 443, HTTP Host header on 80.
package main

import "strings"

// parse_tls_sni returns the SNI server_name from a TLS ClientHello, or ""
// if the buffer isn't a ClientHello or has no SNI. Fully bounds-checked;
// any malformed/short input yields "".
func parse_tls_sni(b []byte) string {
	// TLS record: type(1)=0x16 handshake, version(2), length(2)
	if len(b) < 5 || b[0] != 0x16 {
		return ""
	}
	p := 5
	// Handshake: type(1)=0x01 ClientHello, length(3)
	if len(b) < p+4 || b[p] != 0x01 {
		return ""
	}
	p += 4
	p += 2  // client_version
	p += 32 // random
	if len(b) < p+1 {
		return ""
	}
	p += 1 + int(b[p]) // session_id
	if len(b) < p+2 {
		return ""
	}
	p += 2 + (int(b[p])<<8 | int(b[p+1])) // cipher_suites
	if len(b) < p+1 {
		return ""
	}
	p += 1 + int(b[p]) // compression_methods
	if len(b) < p+2 {
		return ""
	}
	extEnd := p + 2 + (int(b[p])<<8 | int(b[p+1]))
	p += 2
	if extEnd > len(b) {
		extEnd = len(b)
	}

	for p+4 <= extEnd {
		etype := int(b[p])<<8 | int(b[p+1])
		elen := int(b[p+2])<<8 | int(b[p+3])
		p += 4
		if p+elen > extEnd {
			return ""
		}
		if etype == 0x0000 { // server_name
			e := b[p : p+elen]
			// server_name_list: list_len(2); entry: type(1) len(2) name
			if len(e) < 5 || e[2] != 0x00 {
				return ""
			}
			nameLen := int(e[3])<<8 | int(e[4])
			if 5+nameLen > len(e) {
				return ""
			}
			return strings.ToLower(string(e[5 : 5+nameLen]))
		}
		p += elen
	}
	return ""
}

// parse_http_host returns the Host header value (without any :port) from
// the start of an HTTP request, or "".
func parse_http_host(b []byte) string {
	for _, ln := range strings.Split(string(b), "\r\n") {
		if ln == "" {
			break // end of headers
		}
		if len(ln) >= 5 && strings.EqualFold(ln[:5], "host:") {
			h := strings.TrimSpace(ln[5:])
			if i := strings.LastIndexByte(h, ':'); i > 0 && !strings.ContainsRune(h[i:], ']') {
				h = h[:i] // strip :port (but not IPv6 brackets)
			}
			return strings.ToLower(h)
		}
	}
	return ""
}
