// sniff_test.go
package main

import "testing"

// Builds a minimal TLS ClientHello carrying one SNI server_name.
func clientHelloWithSNI(name string) []byte {
	sni := []byte(name)
	// server_name extension body: list_len(2) type(1) name_len(2) name
	ext := []byte{}
	ext = append(ext, byte((len(sni)+3)>>8), byte(len(sni)+3)) // list length
	ext = append(ext, 0x00)                                    // name type: host_name
	ext = append(ext, byte(len(sni)>>8), byte(len(sni)))       // name length
	ext = append(ext, sni...)
	// extension: type(2)=0 len(2) body
	extension := []byte{0x00, 0x00, byte(len(ext) >> 8), byte(len(ext))}
	extension = append(extension, ext...)
	extensions := extension

	body := []byte{}
	body = append(body, 0x03, 0x03)             // client_version TLS1.2
	body = append(body, make([]byte, 32)...)    // random
	body = append(body, 0x00)                   // session_id len 0
	body = append(body, 0x00, 0x02, 0x13, 0x01) // cipher_suites len 2 + one suite
	body = append(body, 0x01, 0x00)             // compression: len1 + null
	body = append(body, byte(len(extensions)>>8), byte(len(extensions)))
	body = append(body, extensions...)

	hs := []byte{0x01, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}
	hs = append(hs, body...)

	rec := []byte{0x16, 0x03, 0x01, byte(len(hs) >> 8), byte(len(hs))}
	rec = append(rec, hs...)
	return rec
}

func TestParseTLSSNI(t *testing.T) {
	if got := parse_tls_sni(clientHelloWithSNI("cdn.example.com")); got != "cdn.example.com" {
		t.Fatalf("want cdn.example.com, got %q", got)
	}
	if got := parse_tls_sni(clientHelloWithSNI("WWW.YouTube.COM")); got != "www.youtube.com" {
		t.Fatalf("SNI should be lowercased, got %q", got)
	}
}

func TestParseTLSSNIRejectsGarbage(t *testing.T) {
	for _, b := range [][]byte{
		nil,
		{0x16},
		{0x17, 0x03, 0x03, 0x00, 0x00}, // not a handshake
		{0x16, 0x03, 0x01, 0x00, 0x04, 0x02, 0, 0, 0}, // handshake type != ClientHello
		clientHelloWithSNI("x")[:10],                  // truncated
	} {
		if got := parse_tls_sni(b); got != "" {
			t.Fatalf("garbage must yield empty, got %q for %v", got, b)
		}
	}
}

func TestParseHTTPHost(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: example.com\r\nUser-Agent: x\r\n\r\n"
	if got := parse_http_host([]byte(req)); got != "example.com" {
		t.Fatalf("want example.com, got %q", got)
	}
	// case-insensitive header, :port stripped
	req2 := "GET / HTTP/1.1\r\nhOsT: Example.COM:8080\r\n\r\n"
	if got := parse_http_host([]byte(req2)); got != "example.com" {
		t.Fatalf("want example.com (port stripped, lowercased), got %q", got)
	}
	// no Host header
	if got := parse_http_host([]byte("GET / HTTP/1.0\r\n\r\n")); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}
