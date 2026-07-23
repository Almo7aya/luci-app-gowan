'use strict';
'require view';
'require form';

return view.extend({
	render: function() {
		var m, s, o;

		m = new form.Map('gowan', _('GoWAN Settings'));

		s = m.section(form.NamedSection, 'main', 'gowan', _('General'));
		s.addremove = false;

		o = s.option(form.Flag, 'enabled', _('Enable GoWAN'));
		o.default = '0';
		o.rmempty = false;

		o = s.option(form.Value, 'listen_host', _('Listen address'),
			_('The SOCKS5 port is guarded by the Access Control rules'));
		o.datatype = 'ip4addr("nomask")';
		o.default = '0.0.0.0';

		o = s.option(form.Value, 'listen_port', _('Listen port'));
		o.datatype = 'port';
		o.default = '1080';

		o = s.option(form.ListValue, 'acl_default', _('ACL default verdict'),
			_('Applied when no Access Control rule matches. "Deny" plus an allow rule for the LAN subnet is the safe default; an allow rule for the LAN is seeded at install.'));
		o.value('deny', _('Deny'));
		o.value('allow', _('Allow'));
		o.default = 'deny';

		o = s.option(form.Value, 'stats_interval', _('Dashboard refresh interval (s)'),
			_('How often the Overview page polls for live stats'));
		o.datatype = 'range(1,60)';
		o.default = '3';

		o = s.option(form.Flag, 'debug', _('Debug logging'),
			_('Log every dispatched connection and UDP flow to the system log. Verbose — leave off in production; enable only when troubleshooting.'));
		o.default = '0';

		s = m.section(form.NamedSection, 'main', 'gowan', _('Health Checks'));

		o = s.option(form.ListValue, 'check_type', _('Check type'),
			_('Global in this release: every backend runs the same check bound to its own interface'));
		o.value('tcp', _('TCP connect'));
		o.value('http', _('HTTP GET'));
		o.value('none', _('Disabled'));
		o.default = 'tcp';

		o = s.option(form.Value, 'check_target', _('Check target'),
			_('host:port for TCP (e.g. 8.8.8.8:53), URL for HTTP'));
		o.default = '8.8.8.8:53';
		o.depends('check_type', 'tcp');
		o.depends('check_type', 'http');

		o = s.option(form.Value, 'check_interval', _('Interval (s)'));
		o.datatype = 'range(1,3600)';
		o.default = '30';
		o.depends('check_type', 'tcp');
		o.depends('check_type', 'http');

		o = s.option(form.Value, 'check_timeout', _('Timeout (s)'));
		o.datatype = 'range(1,60)';
		o.default = '5';
		o.depends('check_type', 'tcp');
		o.depends('check_type', 'http');

		o = s.option(form.Value, 'check_fail_threshold', _('Fail threshold'),
			_('Consecutive failures before a backend is marked down'));
		o.datatype = 'range(1,20)';
		o.default = '3';
		o.depends('check_type', 'tcp');
		o.depends('check_type', 'http');

		o = s.option(form.Value, 'check_rise_threshold', _('Rise threshold'),
			_('Consecutive successes before a backend is marked up again'));
		o.datatype = 'range(1,20)';
		o.default = '2';
		o.depends('check_type', 'tcp');
		o.depends('check_type', 'http');

		s = m.section(form.NamedSection, 'main', 'gowan', _('Status API'),
			_('Local JSON endpoint (127.0.0.1 only) for external monitoring: uptime, per-WAN health and connection counters at /status.'));

		o = s.option(form.Flag, 'api_enabled', _('Enable status API'));
		o.default = '0';

		o = s.option(form.Value, 'api_port', _('API port'));
		o.datatype = 'port';
		o.default = '9080';
		o.depends('api_enabled', '1');

		s = m.section(form.NamedSection, 'main', 'gowan', _('Transparent Mode'),
			_('Intercept ALL TCP traffic from the selected subnets and balance it across the WAN backends — no client configuration needed. Traffic to private/LAN destinations stays direct.'));

		o = s.option(form.Flag, 'transparent', _('Enable transparent interception'));
		o.default = '0';
		o.rmempty = false;

		o = s.option(form.Value, 'transparent_port', _('Internal redirect port'),
			_('Clients never use this port directly'));
		o.datatype = 'port';
		o.default = '1081';
		o.depends('transparent', '1');

		o = s.option(form.DynamicList, 'transparent_subnet', _('Intercept subnets'),
			_('Source subnets whose TCP traffic is intercepted, e.g. the LAN subnet'));
		o.datatype = 'or(ip4addr("nomask"), cidr4)';
		o.placeholder = '10.0.1.0/24';
		o.depends('transparent', '1');

		o = s.option(form.Flag, 'transparent_udp', _('Balance UDP too (experimental)'),
			_('Relays intercepted UDP (DNS, QUIC, games, VoIP) across the WANs via TPROXY, with per-flow affinity. Each flow stays on one WAN; this distributes flows, it does not aggregate a single connection.'));
		o.default = '0';
		o.depends('transparent', '1');

		o = s.option(form.Value, 'transparent_udp_port', _('UDP TPROXY port'),
			_('Internal port for the UDP relay; clients never use it'));
		o.datatype = 'port';
		o.default = '1082';
		o.depends('transparent_udp', '1');

		o = s.option(form.Value, 'udp_timeout', _('UDP flow idle timeout (s)'));
		o.datatype = 'range(5,3600)';
		o.default = '60';
		o.depends('transparent_udp', '1');

		o = s.option(form.Flag, 'block_quic', _('Block QUIC (UDP 443)'),
			_('Forces browsers to fall back from HTTP/3 to TCP so their traffic is balanced. Ignored when UDP balancing is on (QUIC is then carried, not blocked).'));
		o.default = '1';
		o.depends({ transparent: '1', transparent_udp: '0' });

		s = m.section(form.NamedSection, 'main', 'gowan', _('Sticky Sessions'),
			_('Pin each client IP to the same backend for the duration of a session — useful for sites that dislike a session hopping between source IPs.'));

		o = s.option(form.Flag, 'sticky', _('Enable sticky sessions'));
		o.default = '0';

		o = s.option(form.Value, 'sticky_timeout', _('Session timeout (s)'),
			_('A client mapping expires after this many seconds of inactivity'));
		o.datatype = 'range(10,86400)';
		o.default = '300';
		o.depends('sticky', '1');

		// SOCKS5 authentication lives in its own UCI section.
		s = m.section(form.NamedSection, 'auth', 'auth', _('SOCKS5 Authentication'),
			_('Require a username and password on the SOCKS5 listener (RFC 1929). Transparent mode is unaffected — it is guarded by Access Control.'));
		s.anonymous = true;

		o = s.option(form.Flag, 'enabled', _('Require authentication'));
		o.default = '0';

		o = s.option(form.Value, 'username', _('Username'));
		o.depends('enabled', '1');
		o.rmempty = false;

		o = s.option(form.Value, 'password', _('Password'));
		o.password = true;
		o.depends('enabled', '1');
		o.rmempty = false;

		// Failover notifications.
		s = m.section(form.NamedSection, 'alerts', 'notify', _('Failover Notifications'),
			_('Send an alert when a WAN goes down or comes back up. Requires curl on the router.'));
		s.anonymous = true;

		o = s.option(form.Flag, 'enabled', _('Enable notifications'));
		o.default = '0';

		o = s.option(form.ListValue, 'type', _('Channel'));
		o.value('telegram', _('Telegram'));
		o.value('discord', _('Discord webhook'));
		o.value('webhook', _('Generic webhook'));
		o.default = 'telegram';
		o.depends('enabled', '1');

		o = s.option(form.Value, 'telegram_bot_token', _('Telegram bot token'));
		o.depends({ enabled: '1', type: 'telegram' });

		o = s.option(form.Value, 'telegram_chat_id', _('Telegram chat ID'));
		o.depends({ enabled: '1', type: 'telegram' });

		o = s.option(form.Value, 'webhook_url', _('Webhook URL'));
		o.depends({ enabled: '1', type: 'discord' });
		o.depends({ enabled: '1', type: 'webhook' });

		o = s.option(form.Flag, 'on_wan_down', _('Notify on WAN down'));
		o.default = '1';
		o.depends('enabled', '1');

		o = s.option(form.Flag, 'on_wan_up', _('Notify on WAN up'));
		o.default = '1';
		o.depends('enabled', '1');

		return m.render();
	}
});
