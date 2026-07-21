#!/bin/sh
# GoWAN notification sender. Invoked by the daemon's -on-change hook as:
#   gowan-notify.sh <backend-ip> <old-state> <new-state>
# Reads the UCI 'notify' section and dispatches an alert. Fire-and-forget;
# any failure is logged, never fatal (the daemon ignores our exit code).

. /lib/functions.sh
. /usr/share/libubox/jshn.sh

IP="$1"
# $2 is the old state (unused); $3 is the new state.
NEW="$3"
[ -n "$NEW" ] || exit 0

config_load gowan

enabled=0
config_get_bool enabled alerts enabled 0
[ "$enabled" -eq 0 ] && exit 0

# Respect per-direction toggles.
on_up=1; on_down=1
config_get_bool on_up alerts on_wan_up 1
config_get_bool on_down alerts on_wan_down 1
case "$NEW" in
	up)   [ "$on_up" -eq 1 ] || exit 0 ;;
	down) [ "$on_down" -eq 1 ] || exit 0 ;;
esac

# Resolve a friendly backend name from the state file if available.
label="$IP"
if [ -f /var/run/gowan/health.json ]; then
	name=$(jsonfilter -i /var/run/gowan/health.json \
		-e "@.backends[@.ip='$IP'].name" 2>/dev/null)
	[ -n "$name" ] && label="$name ($IP)"
fi

host=$(cat /proc/sys/kernel/hostname 2>/dev/null)
if [ "$NEW" = "down" ]; then
	msg="GoWAN [$host]: WAN $label is DOWN"
else
	msg="GoWAN [$host]: WAN $label is back UP"
fi

command -v curl >/dev/null 2>&1 || {
	logger -t gowan-notify "curl not installed; cannot send notification"
	exit 0
}

ntype=""
config_get ntype alerts type ""
case "$ntype" in
	telegram)
		config_get token alerts telegram_bot_token ""
		config_get chat alerts telegram_chat_id ""
		[ -n "$token" ] && [ -n "$chat" ] || exit 0
		curl -sS -m 10 "https://api.telegram.org/bot${token}/sendMessage" \
			--data-urlencode "chat_id=${chat}" \
			--data-urlencode "text=${msg}" >/dev/null 2>&1
		;;
	discord)
		config_get url alerts webhook_url ""
		[ -n "$url" ] || exit 0
		json_init
		json_add_string content "$msg"
		curl -sS -m 10 -H "Content-Type: application/json" \
			-d "$(json_dump)" "$url" >/dev/null 2>&1
		;;
	webhook)
		config_get url alerts webhook_url ""
		[ -n "$url" ] || exit 0
		json_init
		json_add_string text "$msg"
		curl -sS -m 10 -H "Content-Type: application/json" \
			-d "$(json_dump)" "$url" >/dev/null 2>&1
		;;
	*)
		exit 0
		;;
esac

logger -t gowan-notify "sent $ntype alert: $msg"
exit 0
