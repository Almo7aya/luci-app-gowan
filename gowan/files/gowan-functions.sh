#!/bin/sh
# GoWAN shared shell helpers. Sourced by the init script, hotplug handler
# and rpcd backend — never executed directly.

. /lib/functions.sh
. /lib/functions/network.sh
. /usr/share/libubox/jshn.sh

GOWAN_RUN_DIR=/var/run/gowan
# shellcheck disable=SC2034  # consumed by the scripts sourcing this file
GOWAN_STATE_FILE=$GOWAN_RUN_DIR/health.json

# gowan_resolve_wan <logical-interface> <result-var>
# Resolves the current IPv4 address of an OpenWrt logical interface.
gowan_resolve_wan() {
	local __ip
	network_get_ipaddr __ip "$1" || return 1
	[ -n "$__ip" ] || return 1
	eval "$2=\$__ip"
}

# Emits nft rules for one ACL section (all its subnets). Runs inside
# gowan_apply_acl's config_foreach; output is collected into the ruleset.
_gowan_emit_acl_rule() {
	local section="$1" enabled verdict
	config_get_bool enabled "$section" enabled 1
	[ "$enabled" -eq 0 ] && return 0
	config_get verdict "$section" verdict allow

	_gowan_emit_subnet() {
		case "$1" in
			*[!0-9./]*) return 0 ;;  # refuse anything that is not an IPv4 CIDR
		esac
		if [ "$verdict" = "allow" ]; then
			printf '\t\tip saddr %s accept\n' "$1"
		else
			printf '\t\tip saddr %s reject with tcp reset\n' "$1"
		fi
	}
	config_list_foreach "$section" subnet _gowan_emit_subnet
}

# Destination ranges that must never be intercepted: local, private,
# CGNAT, link-local, multicast, broadcast. LAN-to-LAN and LAN-to-router
# traffic stays direct.
GOWAN_NO_INTERCEPT='0.0.0.0/8, 10.0.0.0/8, 100.64.0.0/10, 127.0.0.0/8, 169.254.0.0/16, 172.16.0.0/12, 192.168.0.0/16, 224.0.0.0/4, 240.0.0.0/4'

# Validates and joins the transparent_subnet list into "a, b, c" form
# for an nft anonymous set. Empty result = nothing valid to intercept.
_gowan_transparent_subnets() {
	local subnets="" s
	config_get s main transparent_subnet
	for s in $s; do
		case "$s" in
			*[!0-9./]*) continue ;;
		esac
		subnets="${subnets:+$subnets, }$s"
	done
	echo "$subnets"
}

# TPROXY plumbing: locally-delivered marked packets need their own
# routing table so the kernel hands them to the transparent socket.
GOWAN_TPROXY_MARK=0x1
GOWAN_TPROXY_TABLE=100

gowan_apply_tproxy_route() {
	ip rule list 2>/dev/null | grep -q "fwmark $GOWAN_TPROXY_MARK lookup $GOWAN_TPROXY_TABLE" || \
		ip rule add fwmark "$GOWAN_TPROXY_MARK" lookup "$GOWAN_TPROXY_TABLE" 2>/dev/null
	ip route show table "$GOWAN_TPROXY_TABLE" 2>/dev/null | grep -q "local default" || \
		ip route add local default dev lo table "$GOWAN_TPROXY_TABLE" 2>/dev/null
}

gowan_teardown_tproxy_route() {
	ip rule del fwmark "$GOWAN_TPROXY_MARK" lookup "$GOWAN_TPROXY_TABLE" 2>/dev/null
	ip route flush table "$GOWAN_TPROXY_TABLE" 2>/dev/null
	return 0
}

# gowan_apply_nft <socks-port> <transparent> <tport> <block-quic> <udp> <udp-port>
# Materializes the whole gowan ruleset into a dedicated nftables table:
# input-chain ACL guarding the listener ports, plus (when enabled) the
# transparent TCP redirect, the UDP TPROXY chain, and the QUIC block.
# Idempotent — replaces any previous gowan table. Independent from fw4's
# table, so firewall reloads never wipe it.
gowan_apply_nft() {
	local socks_port="$1" transparent="$2" tport="$3" block_quic="$4"
	local udp="$5" udp_port="$6"
	local default rules tail_rule="" guarded_ports subnets extra=""

	case "$socks_port" in ''|*[!0-9]*) return 1 ;; esac
	case "$tport" in ''|*[!0-9]*) return 1 ;; esac
	case "$udp_port" in ''|*[!0-9]*) udp_port=0 ;; esac

	config_get default main acl_default deny
	rules="$(config_foreach _gowan_emit_acl_rule acl)"
	[ "$default" = "deny" ] && tail_rule="		reject with tcp reset"

	guarded_ports="$socks_port"
	if [ "$transparent" = "1" ]; then
		guarded_ports="$socks_port, $tport"
		subnets="$(_gowan_transparent_subnets)"

		if [ -n "$subnets" ]; then
			extra="
	chain transparent {
		type nat hook prerouting priority dstnat; policy accept;
		ip daddr { $GOWAN_NO_INTERCEPT } return
		ip saddr { $subnets } meta l4proto tcp redirect to :$tport
	}"
			# UDP relay via TPROXY (mangle-priority prerouting). When on,
			# we carry QUIC rather than blocking it.
			if [ "$udp" = "1" ] && [ "$udp_port" != "0" ]; then
				gowan_apply_tproxy_route
				extra="$extra
	chain udp_tproxy {
		type filter hook prerouting priority mangle; policy accept;
		ip daddr { $GOWAN_NO_INTERCEPT } return
		ip saddr { $subnets } meta l4proto udp tproxy ip to :$udp_port meta mark set $GOWAN_TPROXY_MARK
	}"
			elif [ "$block_quic" = "1" ]; then
				extra="$extra
	chain quic {
		type filter hook forward priority 0; policy accept;
		ip daddr { $GOWAN_NO_INTERCEPT } return
		ip saddr { $subnets } udp dport 443 reject
	}"
			fi
		else
			logger -t gowan "transparent mode enabled but no valid transparent_subnet configured, not intercepting"
		fi
	fi

	nft -f - <<-EOF
		table inet gowan
		delete table inet gowan
		table inet gowan {
			chain input {
				type filter hook input priority -1; policy accept;
				tcp dport { $guarded_ports } jump acl
			}
			chain acl {
				iifname "lo" accept
		$rules
		$tail_rule
			}
		$extra
		}
	EOF
}

gowan_teardown_acl() {
	nft delete table inet gowan 2>/dev/null
	gowan_teardown_tproxy_route
	return 0
}

# Emits one backend object into the jshn document being built by
# gowan_render_backends. Per-WAN check_* options are optional overrides;
# absent fields inherit the daemon's global -check-* flags.
_gowan_render_wan() {
	local section="$1" enabled iface ratio ip
	local ctype ctarget cint ctmo cfail crise

	config_get_bool enabled "$section" enabled 1
	[ "$enabled" -eq 0 ] && return 0
	config_get iface "$section" interface
	[ -n "$iface" ] || return 0

	if ! gowan_resolve_wan "$iface" ip; then
		logger -t gowan "WAN $section: interface $iface has no IPv4 address, skipping"
		return 0
	fi
	config_get ratio "$section" ratio 1

	config_get ctype "$section" check_type ""
	config_get ctarget "$section" check_target ""
	config_get cint "$section" check_interval ""
	config_get ctmo "$section" check_timeout ""
	config_get cfail "$section" check_fail_threshold ""
	config_get crise "$section" check_rise_threshold ""

	json_add_object ""
	json_add_string name "$section"
	json_add_string ip "$ip"
	json_add_int ratio "$ratio"
	if [ -n "$ctype$ctarget$cint$ctmo$cfail$crise" ]; then
		json_add_object check
		[ -n "$ctype" ] && json_add_string type "$ctype"
		[ -n "$ctarget" ] && json_add_string target "$ctarget"
		[ -n "$cint" ] && json_add_int interval "$cint"
		[ -n "$ctmo" ] && json_add_int timeout "$ctmo"
		[ -n "$cfail" ] && json_add_int fail "$cfail"
		[ -n "$crise" ] && json_add_int rise "$crise"
		json_close_object
	fi
	json_close_object

	_gowan_backend_count=$((_gowan_backend_count + 1))
}

# Renders the daemon's backends file from UCI wan sections. Caller must
# have run config_load gowan and network_flush_cache. Returns 1 when no
# WAN is usable (file left untouched so the daemon keeps its last set).
gowan_render_backends() {
	_gowan_backend_count=0

	json_init
	json_add_array backends
	config_foreach _gowan_render_wan wan
	json_close_array

	[ "$_gowan_backend_count" -gt 0 ] || return 1

	json_dump > "$GOWAN_RUN_DIR/backends.json.tmp" &&
		mv "$GOWAN_RUN_DIR/backends.json.tmp" "$GOWAN_RUN_DIR/backends.json"
}

_gowan_render_policy() {
	local section="$1" enabled ptype match wan
	config_get_bool enabled "$section" enabled 1
	[ "$enabled" -eq 0 ] && return 0
	config_get ptype "$section" type ""
	config_get match "$section" match ""
	config_get wan "$section" wan ""
	# Only client_ip is enforced by the daemon today.
	[ "$ptype" = "client_ip" ] || return 0
	[ -n "$match" ] && [ -n "$wan" ] || return 0

	json_add_object ""
	json_add_string type "$ptype"
	json_add_string match "$match"
	json_add_string wan "$wan"
	json_close_object
}

# Renders the daemon's policy file from UCI policy sections. Always
# writes a valid file (possibly an empty list).
gowan_render_policies() {
	json_init
	json_add_array policies
	config_foreach _gowan_render_policy policy
	json_close_array
	json_dump > "$GOWAN_RUN_DIR/policies.json.tmp" &&
		mv "$GOWAN_RUN_DIR/policies.json.tmp" "$GOWAN_RUN_DIR/policies.json"
}
