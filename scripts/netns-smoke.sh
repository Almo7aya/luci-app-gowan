#!/bin/sh
# GoWAN integration smoke test (run as root, e.g. in CI).
#
# Builds two fake "WANs" as network namespaces reachable through separate
# veth pairs, serves a distinct payload behind each, then asserts:
#   1. connections through the SOCKS5 proxy are balanced per contention ratio
#   2. killing one WAN flips it DOWN in health.json, fires the on-change
#      hook, and traffic keeps flowing through the survivor
#   3. restoring the WAN flips it back UP and it rejoins the rotation
#
# Topology (host side IPs are the daemon's backends):
#   host v1h 10.201.1.1/24 <-> ns1 v1n 10.201.1.2/24   [serves "wan1"]
#   host v2h 10.201.2.1/24 <-> ns2 v2n 10.201.2.2/24   [serves "wan2"]
#   10.99.99.99 lives on the loopback of BOTH namespaces; the host routes
#   it via ns1 (metric 100) and ns2 (metric 200). SO_BINDTODEVICE decides
#   which path a given dial actually takes.

set -eu

SRC_DIR=$(dirname "$0")/../gowan/src
WORK=$(mktemp -d)
PROXY_PORT=11080
TARGET=10.99.99.99
STATE="$WORK/health.json"
HOOK_LOG="$WORK/hook.log"
DAEMON_LOG="$WORK/daemon.log"
DAEMON_PID=""

fail() {
	echo "FAIL: $*" >&2
	echo "--- daemon log ---" >&2
	cat "$DAEMON_LOG" 2>/dev/null >&2 || true
	exit 1
}

cleanup() {
	if [ -n "$DAEMON_PID" ]; then
		kill "$DAEMON_PID" 2>/dev/null || true
	fi
	ip netns del gowan-ns1 2>/dev/null || true
	ip netns del gowan-ns2 2>/dev/null || true
	rm -rf "$WORK"
}
trap cleanup EXIT INT TERM

[ "$(id -u)" = "0" ] || { echo "must run as root"; exit 1; }

echo "== building gowan"
( cd "$SRC_DIR" && go build -o "$WORK/gowan" . )

echo "== setting up namespaces"
setup_wan() {
	# $1 = index, $2 = host ip, $3 = ns ip, $4 = payload
	ip netns add "gowan-ns$1"
	ip link add "v${1}h" type veth peer name "v${1}n"
	ip link set "v${1}n" netns "gowan-ns$1"
	ip addr add "$2/24" dev "v${1}h"
	ip link set "v${1}h" up
	ip netns exec "gowan-ns$1" ip addr add "$3/24" dev "v${1}n"
	ip netns exec "gowan-ns$1" ip link set "v${1}n" up
	ip netns exec "gowan-ns$1" ip link set lo up
	ip netns exec "gowan-ns$1" ip addr add "$TARGET/32" dev lo
	ip netns exec "gowan-ns$1" ip route add default via "$2"
	ip netns exec "gowan-ns$1" sysctl -qw net.ipv4.conf.all.rp_filter=0 \
		"net.ipv4.conf.v${1}n.rp_filter=0"
	ip route add "$TARGET/32" via "$3" dev "v${1}h" metric "${1}00"

	mkdir -p "$WORK/www$1"
	printf '%s' "$4" > "$WORK/www$1/index.html"
	ip netns exec "gowan-ns$1" python3 -m http.server 8080 \
		--bind "$TARGET" --directory "$WORK/www$1" >/dev/null 2>&1 &
}

setup_wan 1 10.201.1.1 10.201.1.2 wan1
setup_wan 2 10.201.2.1 10.201.2.2 wan2
sleep 1

cat > "$WORK/hook.sh" <<EOF
#!/bin/sh
echo "\$1 \$2 \$3" >> $HOOK_LOG
EOF
chmod +x "$WORK/hook.sh"

echo "== starting daemon"
"$WORK/gowan" -lhost 127.0.0.1 -lport $PROXY_PORT \
	-check-type tcp -check-target "$TARGET:8080" \
	-check-interval 1 -check-timeout 2 -check-fail 2 -check-rise 2 \
	-state-file "$STATE" -on-change "$WORK/hook.sh" \
	10.201.1.1@1 10.201.2.1@1 > "$DAEMON_LOG" 2>&1 &
DAEMON_PID=$!
sleep 2
kill -0 "$DAEMON_PID" 2>/dev/null || fail "daemon did not start"

fetch() {
	curl -s --max-time 5 --socks5 "127.0.0.1:$PROXY_PORT" \
		"http://$TARGET:8080/index.html" || true
}

backend_status() {
	python3 -c "
import json, sys
doc = json.load(open('$STATE'))
for b in doc['backends']:
    if b['ip'] == '$1':
        print(b['status']); sys.exit(0)
sys.exit(1)
"
}

wait_status() {
	# $1 = backend ip, $2 = wanted status, $3 = timeout seconds
	i=0
	while [ "$i" -lt "$3" ]; do
		[ "$(backend_status "$1" 2>/dev/null)" = "$2" ] && return 0
		sleep 1
		i=$((i + 1))
	done
	return 1
}

echo "== test 1: ratio distribution"
hits1=0; hits2=0
for _ in 1 2 3 4 5 6 7 8 9 10; do
	body=$(fetch)
	case "$body" in
		wan1) hits1=$((hits1 + 1)) ;;
		wan2) hits2=$((hits2 + 1)) ;;
		*) fail "unexpected response '$body'" ;;
	esac
done
echo "   wan1=$hits1 wan2=$hits2"
[ "$hits1" -eq 5 ] || fail "1:1 ratio must split 10 requests 5/5, got $hits1/$hits2"

echo "== test 2: failover"
ip link set v1h down
wait_status 10.201.1.1 down 15 || fail "backend 10.201.1.1 never flipped DOWN"
grep -q "10.201.1.1 up down" "$HOOK_LOG" || fail "on-change hook did not fire for DOWN flip"

for _ in 1 2 3 4 5; do
	body=$(fetch)
	[ "$body" = "wan2" ] || fail "expected all traffic on wan2 during outage, got '$body'"
done
echo "   survivor carried all traffic"

echo "== test 3: recovery"
ip link set v1h up
ip route replace "$TARGET/32" via 10.201.1.2 dev v1h metric 100
wait_status 10.201.1.1 up 15 || fail "backend 10.201.1.1 never recovered"
grep -q "10.201.1.1 down up" "$HOOK_LOG" || fail "on-change hook did not fire for UP flip"

recovered=0
for _ in 1 2 3 4 5 6 7 8 9 10; do
	[ "$(fetch)" = "wan1" ] && recovered=1
done
[ "$recovered" -eq 1 ] || fail "wan1 never rejoined the rotation after recovery"
echo "   backend rejoined rotation"

echo "PASS: all integration assertions held"
