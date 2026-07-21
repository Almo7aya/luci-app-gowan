#!/bin/sh
# gowan-speedtest.sh <wan-section>
# Runs a quick download + latency test bound to one WAN's interface and
# prints a JSON result. Used by the rpcd 'speedtest' method.

. /lib/functions.sh
. /lib/functions/network.sh
. /usr/share/libubox/jshn.sh

SECTION="$1"
TEST_URL="${GOWAN_SPEEDTEST_URL:-https://speed.cloudflare.com/__down?bytes=10000000}"
PING_TARGET="${GOWAN_SPEEDTEST_PING:-8.8.8.8}"
TIMEOUT=20

emit() { # status [mbps latency]
	json_init
	json_add_string wan "$SECTION"
	json_add_string status "$1"
	json_add_double mbps "${2:-0}"
	json_add_int latency_ms "${3:-0}"
	json_dump
}

# Validate the section name against actual config before use.
config_load gowan
found=0
_match() { [ "$1" = "$SECTION" ] && found=1; }
config_foreach _match wan
[ "$found" -eq 1 ] || { emit "invalid_section"; exit 0; }

config_get iface "$SECTION" interface
[ -n "$iface" ] || { emit "no_interface"; exit 0; }
device=""
network_get_device device "$iface" || { emit "no_device"; exit 0; }
[ -n "$device" ] || { emit "no_device"; exit 0; }

command -v curl >/dev/null 2>&1 || { emit "no_curl"; exit 0; }

# Latency via interface-bound ping (best effort; carriers may block ICMP).
latency=$(ping -c3 -W2 -I "$device" "$PING_TARGET" 2>/dev/null | \
	awk -F'/' 'END{ if ($5 != "") printf "%.0f", $5 }')

# Download speed bound to the interface device.
speed_bps=$(curl -sS --interface "$device" --max-time "$TIMEOUT" \
	-o /dev/null -w '%{speed_download}' "$TEST_URL" 2>/dev/null)

if [ -z "$speed_bps" ] || [ "$speed_bps" = "0" ] || [ "$speed_bps" = "0.000" ]; then
	emit "failed" 0 "${latency:-0}"
	exit 0
fi

mbps=$(awk -v b="$speed_bps" 'BEGIN{ printf "%.2f", b*8/1000000 }')
emit "ok" "$mbps" "${latency:-0}"
exit 0
