#!/bin/sh
# gowan-speedtest.sh <wan-section>
# Runs a download + latency test bound to one WAN's interface and writes
# the result to /var/run/gowan/speedtest/<section>.json. Meant to be
# launched DETACHED by the rpcd backend so the test survives the client
# leaving the page; a lock dir marks it as running. Speed is reported in
# bytes/sec (bytes_per_sec).

. /lib/functions.sh
. /lib/functions/network.sh
. /usr/share/libubox/jshn.sh

SECTION="$1"
DIR=/var/run/gowan/speedtest
RESULT="$DIR/$SECTION.json"
LOCK="$DIR/$SECTION.lock"
TEST_URL="${GOWAN_SPEEDTEST_URL:-https://speed.cloudflare.com/__down?bytes=10000000}"
PING_TARGET="${GOWAN_SPEEDTEST_PING:-8.8.8.8}"
TIMEOUT=20

mkdir -p "$DIR"

# Serialize per section; a running test owns the lock dir.
if ! mkdir "$LOCK" 2>/dev/null; then
	exit 0
fi
# shellcheck disable=SC2064  # expand LOCK now, on trap install
trap "rmdir '$LOCK' 2>/dev/null" EXIT INT TERM

now() { date +%s 2>/dev/null || echo 0; }

write_result() { # status bytes_per_sec latency
	json_init
	json_add_string wan "$SECTION"
	json_add_string status "$1"
	json_add_double bytes_per_sec "${2:-0}"
	json_add_int latency_ms "${3:-0}"
	json_add_int ts "$(now)"
	json_dump > "$RESULT.tmp" && mv "$RESULT.tmp" "$RESULT"
}

# Validate the section against real config before touching anything.
config_load gowan
found=0
_match() { [ "$1" = "$SECTION" ] && found=1; }
config_foreach _match wan
[ "$found" -eq 1 ] || { write_result "invalid_section"; exit 0; }

config_get iface "$SECTION" interface
[ -n "$iface" ] || { write_result "no_interface"; exit 0; }
device=""
network_get_device device "$iface" || { write_result "no_device"; exit 0; }
[ -n "$device" ] || { write_result "no_device"; exit 0; }

command -v curl >/dev/null 2>&1 || { write_result "no_curl"; exit 0; }

# Latency via interface-bound ping (best effort; carriers may block ICMP).
latency=$(ping -c3 -W2 -I "$device" "$PING_TARGET" 2>/dev/null | \
	awk -F'/' 'END{ if ($5 != "") printf "%.0f", $5 }')

# Download bound to the interface; speed_download is already bytes/sec.
speed_bps=$(curl -sS --interface "$device" --max-time "$TIMEOUT" \
	-o /dev/null -w '%{speed_download}' "$TEST_URL" 2>/dev/null)
speed_bps=$(awk -v b="$speed_bps" 'BEGIN{ printf "%.0f", b+0 }')

if [ -z "$speed_bps" ] || [ "$speed_bps" = "0" ]; then
	write_result "failed" 0 "${latency:-0}"
	exit 0
fi

write_result "ok" "$speed_bps" "${latency:-0}"
exit 0
