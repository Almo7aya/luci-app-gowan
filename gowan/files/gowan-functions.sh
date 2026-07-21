#!/bin/sh
# GoWAN shared shell helpers. Sourced by the init script, hotplug handler
# and rpcd backend — never executed directly.

. /lib/functions.sh
. /lib/functions/network.sh

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

# gowan_apply_acl <listen-port>
# Materializes the ACL into a dedicated nftables table guarding the SOCKS
# port on the input chain. Idempotent: replaces any previous gowan table.
# Independent from fw4's table, so firewall reloads never wipe it.
gowan_apply_acl() {
	local port="$1" default rules tail_rule=""

	case "$port" in
		''|*[!0-9]*) return 1 ;;
	esac

	config_get default main acl_default deny
	rules="$(config_foreach _gowan_emit_acl_rule acl)"
	[ "$default" = "deny" ] && tail_rule="		reject with tcp reset"

	nft -f - <<-EOF
		table inet gowan
		delete table inet gowan
		table inet gowan {
			chain input {
				type filter hook input priority -1; policy accept;
				tcp dport $port jump acl
			}
			chain acl {
				iifname "lo" accept
		$rules
		$tail_rule
			}
		}
	EOF
}

gowan_teardown_acl() {
	nft delete table inet gowan 2>/dev/null
	return 0
}
