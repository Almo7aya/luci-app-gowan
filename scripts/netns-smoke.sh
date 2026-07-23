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
TRANS_PORT=11081
UDP_PORT=11082
TARGET=10.99.99.99
STATE="$WORK/health.json"
HOOK_LOG="$WORK/hook.log"
DAEMON_LOG="$WORK/daemon.log"
DAEMON_PID=""
DAEMON2_PID=""

fail() {
	echo "FAIL: $*" >&2
	echo "--- daemon log ---" >&2
	cat "$DAEMON_LOG" >&2 2>/dev/null || true
	echo "--- server logs ---" >&2
	cat "$WORK"/server*.log >&2 2>/dev/null || true
	echo "--- ns1 listeners ---" >&2
	ip netns exec gowan-ns1 ss -tlnp >&2 2>/dev/null || true
	exit 1
}

cleanup() {
	if [ -n "$DAEMON_PID" ]; then
		kill "$DAEMON_PID" 2>/dev/null || true
	fi
	if [ -n "$DAEMON2_PID" ]; then
		kill "$DAEMON2_PID" 2>/dev/null || true
	fi
	nft delete table inet gowantest 2>/dev/null || true
	nft delete table inet gowanudp 2>/dev/null || true
	ip rule del fwmark 0x1 lookup 100 2>/dev/null || true
	ip route flush table 100 2>/dev/null || true
	# ip netns del does NOT kill processes inside the namespace — the
	# http servers would linger (and pin the old netns) forever.
	ip netns pids gowan-ns1 2>/dev/null | xargs -r kill 2>/dev/null || true
	ip netns pids gowan-ns2 2>/dev/null | xargs -r kill 2>/dev/null || true
	ip netns pids gowan-cli 2>/dev/null | xargs -r kill 2>/dev/null || true
	ip netns del gowan-ns1 2>/dev/null || true
	ip netns del gowan-ns2 2>/dev/null || true
	ip netns del gowan-cli 2>/dev/null || true
	rm -rf "$WORK"
}
trap cleanup EXIT INT TERM

[ "$(id -u)" = "0" ] || { echo "must run as root"; exit 1; }

echo "== building gowan"
( cd "$SRC_DIR" && go build -o "$WORK/gowan" . )

echo "== setting up namespaces"
# Remove leftovers from a previous aborted run so re-runs are clean.
ip netns del gowan-ns1 2>/dev/null || true
ip netns del gowan-ns2 2>/dev/null || true
ip link del v1h 2>/dev/null || true
ip link del v2h 2>/dev/null || true
ip route del "$TARGET/32" 2>/dev/null || true
ip route del "$TARGET/32" 2>/dev/null || true
nft delete table inet gowantest 2>/dev/null || true
ip netns del gowan-cli 2>/dev/null || true
ip link del vch 2>/dev/null || true
ip rule del fwmark 0x1 lookup 100 2>/dev/null || true
ip route flush table 100 2>/dev/null || true

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

	ip netns exec "gowan-ns$1" python3 "$WORK/srv.py" "$TARGET" 8080 "$4" \
		> "$WORK/server$1.log" 2>&1 &
	# UDP echo returning the WAN's payload — for the transparent-UDP test.
	ip netns exec "gowan-ns$1" python3 "$WORK/srv_udp.py" "$TARGET" 9999 "$4" \
		> "$WORK/userver$1.log" 2>&1 &
}

cat > "$WORK/srv_udp.py" <<'EOF'
import socket, sys
addr, port, payload = sys.argv[1], int(sys.argv[2]), sys.argv[3].encode()
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind((addr, port))
while True:
    data, peer = s.recvfrom(2048)
    s.sendto(payload, peer)
EOF

# UDP client: sends N datagrams from distinct source ports (distinct
# flows) and reports which WAN answered each. Prints "wanX" lines.
cat > "$WORK/cli_udp.py" <<'EOF'
import socket, sys
target, port, n = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
for _ in range(n):
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    s.settimeout(3)
    try:
        s.sendto(b"ping", (target, port))
        data, _ = s.recvfrom(2048)
        print(data.decode())
    except socket.timeout:
        print("timeout")
    finally:
        s.close()
EOF

# Minimal HTTP responder. Deliberately NOT python3 -m http.server: that
# blocks in socket.getfqdn() (reverse DNS) between bind() and listen(),
# which hangs forever inside a namespace without working DNS — the
# socket sits bound-but-not-listening and every connect gets RST.
cat > "$WORK/srv.py" <<'EOF'
import socket, sys
addr, port, payload = sys.argv[1], int(sys.argv[2]), sys.argv[3].encode()
s = socket.socket()
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind((addr, port))
s.listen(16)
resp = (b"HTTP/1.0 200 OK\r\nContent-Length: %d\r\n\r\n" % len(payload)) + payload
while True:
    c, _ = s.accept()
    try:
        c.settimeout(2)
        try:
            c.recv(1024)
        except OSError:
            pass
        c.sendall(resp)
    except OSError:
        pass
    finally:
        c.close()
EOF

setup_wan 1 10.201.1.1 10.201.1.2 wan1
setup_wan 2 10.201.2.1 10.201.2.2 wan2

# Client namespace whose traffic is FORWARDED through the host — required
# because TPROXY only works in the prerouting hook, not for locally
# generated packets. Client: 10.202.0.2, default route via host 10.202.0.1.
ip netns add gowan-cli
ip link add vch type veth peer name vcn
ip link set vcn netns gowan-cli
ip addr add 10.202.0.1/24 dev vch
ip link set vch up
ip netns exec gowan-cli ip addr add 10.202.0.2/24 dev vcn
ip netns exec gowan-cli ip link set vcn up
ip netns exec gowan-cli ip link set lo up
ip netns exec gowan-cli ip route add default via 10.202.0.1
sysctl -qw net.ipv4.ip_forward=1
sysctl -qw net.ipv4.conf.all.rp_filter=0 net.ipv4.conf.vch.rp_filter=0
sleep 1

cat > "$WORK/hook.sh" <<EOF
#!/bin/sh
echo "\$1 \$2 \$3" >> $HOOK_LOG
EOF
chmod +x "$WORK/hook.sh"

echo "== starting daemon"
cat > "$WORK/backends.json" <<'EOF'
{"backends": [
  {"name": "wan1", "ip": "10.201.1.1", "ratio": 1},
  {"name": "wan2", "ip": "10.201.2.1", "ratio": 1}
]}
EOF

# 0.0.0.0 matches production: OUTPUT-hook REDIRECT may rewrite the
# destination to a non-loopback local address.
"$WORK/gowan" -lhost 0.0.0.0 -lport $PROXY_PORT -transparent $TRANS_PORT \
	-transparent-udp $UDP_PORT -udp-timeout 30 \
	-backends-file "$WORK/backends.json" -api "127.0.0.1:11090" \
	-check-type tcp -check-target "$TARGET:8080" \
	-check-interval 1 -check-timeout 2 -check-fail 2 -check-rise 2 \
	-state-file "$STATE" -on-change "$WORK/hook.sh" > "$DAEMON_LOG" 2>&1 &
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

echo "== test 4: transparent mode (SO_ORIGINAL_DST via nft redirect)"
# Local curls traverse the OUTPUT hook, so redirect there — same
# conntrack mechanics as the router's prerouting redirect. Scope the
# rule to non-root sockets: on a router the daemon's own dials never
# traverse the prerouting chain, but in this OUTPUT-hook simulation
# they would match and loop the proxy into itself — skuid emulates the
# hook-placement exemption. The client curls run as nobody.
nft add table inet gowantest
nft "add chain inet gowantest out { type nat hook output priority -100 ; }"
nft "add rule inet gowantest out meta skuid != 0 ip daddr $TARGET tcp dport 8080 redirect to :$TRANS_PORT"

thits1=0; thits2=0
for _ in 1 2 3 4 5 6 7 8 9 10; do
	body=$(runuser -u nobody -- curl -s --max-time 5 "http://$TARGET:8080/index.html" || true)
	case "$body" in
		wan1) thits1=$((thits1 + 1)) ;;
		wan2) thits2=$((thits2 + 1)) ;;
		*) fail "transparent: unexpected response '$body'" ;;
	esac
done
echo "   transparent wan1=$thits1 wan2=$thits2"
[ "$thits1" -eq 5 ] || fail "transparent 1:1 ratio must split 10 requests 5/5, got $thits1/$thits2"

# Direct connections to the transparent port (no redirect) must be
# dropped, not looped back into the proxy.
nft delete table inet gowantest
direct=$(curl -s --max-time 5 "http://127.0.0.1:$TRANS_PORT/" || true)
[ -z "$direct" ] || fail "direct connection to transparent port returned data: '$direct'"
echo "   direct connection to transparent port correctly dropped"

echo "== test 5: SIGHUP hot reload"
cat > "$WORK/backends.json" <<'EOF'
{"backends": [{"name": "wan1", "ip": "10.201.1.1", "ratio": 1}]}
EOF
kill -HUP "$DAEMON_PID"
sleep 1
kill -0 "$DAEMON_PID" 2>/dev/null || fail "daemon died on SIGHUP"
for _ in 1 2 3 4; do
	body=$(fetch)
	[ "$body" = "wan1" ] || fail "after dropping wan2 via SIGHUP, got '$body'"
done
echo "   backend removed via SIGHUP, same PID"

cat > "$WORK/backends.json" <<'EOF'
{"backends": [
  {"name": "wan1", "ip": "10.201.1.1", "ratio": 1},
  {"name": "wan2", "ip": "10.201.2.1", "ratio": 1}
]}
EOF
kill -HUP "$DAEMON_PID"
sleep 1
seen2=0
for _ in 1 2 3 4 5 6; do
	[ "$(fetch)" = "wan2" ] && seen2=1
done
[ "$seen2" -eq 1 ] || fail "wan2 never returned after SIGHUP re-add"
echo "   backend re-added via SIGHUP"

echo "== test 6: status API"
api=$(curl -s --max-time 5 "http://127.0.0.1:11090/status" || true)
echo "$api" | grep -q '"backends"' || fail "status API returned no backends: '$api'"
echo "$api" | grep -q '"wan1"' || fail "status API missing wan1: '$api'"
echo "   /status serves backend JSON"

echo "== test 7+8: SOCKS5 auth + client-IP policy (second daemon)"
# A second daemon on its own port with auth on and a policy pinning the
# local client (127.0.0.1) to wan2. Keeps tests 1-6 policy-free.
cat > "$WORK/policy.json" <<'EOF'
{"policies": [{"type": "client_ip", "match": "127.0.0.1", "wan": "wan2"}]}
EOF
"$WORK/gowan" -lhost 127.0.0.1 -lport 11082 \
	-backends-file "$WORK/backends.json" -policy-file "$WORK/policy.json" \
	-auth-user gowan -auth-pass s3cret -check-type none \
	> "$WORK/daemon2.log" 2>&1 &
DAEMON2_PID=$!
sleep 1
kill -0 "$DAEMON2_PID" 2>/dev/null || fail "second daemon did not start"

# No credentials: must be refused.
if curl -s --max-time 5 --socks5 127.0.0.1:11082 "http://$TARGET:8080/index.html" >/dev/null 2>&1; then
	fail "auth: unauthenticated request should have been refused"
fi
echo "   unauthenticated request refused"

# Wrong credentials: refused.
if curl -s --max-time 5 --proxy-user gowan:wrong \
	--socks5 127.0.0.1:11082 "http://$TARGET:8080/index.html" >/dev/null 2>&1; then
	fail "auth: wrong password should have been refused"
fi
echo "   wrong password refused"

# Correct credentials: succeeds, and policy pins the client to wan2.
for _ in 1 2 3 4; do
	body=$(curl -s --max-time 5 --proxy-user gowan:s3cret \
		--socks5 127.0.0.1:11082 "http://$TARGET:8080/index.html" || true)
	[ "$body" = "wan2" ] || fail "auth+policy: expected wan2, got '$body'"
done
echo "   authenticated request pinned to wan2 by policy"

kill "$DAEMON2_PID" 2>/dev/null || true

echo "== test 9: transparent UDP relay (TPROXY)"
# TPROXY needs the nft_tproxy kernel module; skip cleanly if unavailable.
ip rule add fwmark 0x1 lookup 100 2>/dev/null || true
ip route add local default dev lo table 100 2>/dev/null || true
if nft -f - <<-EOF 2>/dev/null
	table inet gowanudp {
		chain pre {
			type filter hook prerouting priority mangle; policy accept;
			ip saddr 10.202.0.0/24 meta l4proto udp tproxy ip to :$UDP_PORT meta mark set 0x1
		}
	}
	EOF
then
	udp_out=$(ip netns exec gowan-cli python3 "$WORK/cli_udp.py" "$TARGET" 9999 8)
	echo "   client saw: $(echo "$udp_out" | sort | uniq -c | tr '\n' ' ')"
	if echo "$udp_out" | grep -q timeout; then
		fail "transparent UDP: some datagrams timed out (no round-trip): $udp_out"
	fi
	echo "$udp_out" | grep -q wan1 || fail "transparent UDP: wan1 never answered"
	echo "$udp_out" | grep -q wan2 || fail "transparent UDP: wan2 never answered"
	echo "   UDP round-trip works and flows are balanced across both WANs"
	nft delete table inet gowanudp 2>/dev/null || true
else
	echo "   SKIP: nft tproxy unavailable on this kernel"
fi

echo "PASS: all integration assertions held"
