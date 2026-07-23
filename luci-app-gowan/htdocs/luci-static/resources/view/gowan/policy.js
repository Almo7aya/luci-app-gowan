'use strict';
'require view';
'require form';
'require uci';

return view.extend({
	load: function() {
		return uci.load('gowan');
	},

	render: function() {
		var m, s, o;

		m = new form.Map('gowan', _('Policy Rules'),
			_('Pin traffic to a specific WAN backend by client IP, destination IP, destination port, or domain. Rules are evaluated top-to-bottom; the first match wins, everything else is load-balanced normally. If a pinned backend is down, its traffic falls back to a healthy one.'));

		s = m.section(form.GridSection, 'policy');
		s.addremove = true;
		s.anonymous = true;
		s.sortable = true;
		s.nodescriptions = true;
		s.addbtntitle = _('Add rule');

		o = s.option(form.Flag, 'enabled', _('Enabled'));
		o.default = '1';
		o.rmempty = false;

		o = s.option(form.Value, 'name', _('Name'));
		o.placeholder = _('e.g. Torrents via WAN 2');

		o = s.option(form.ListValue, 'type', _('Type'));
		o.value('client_ip', _('Client IP'));
		o.value('dest_ip', _('Destination IP'));
		o.value('port', _('Destination port'));
		o.value('domain', _('Domain'));
		o.default = 'client_ip';

		o = s.option(form.Value, 'match', _('Match'),
			_('Comma-separated list. Client/Dest IP: address or CIDR (e.g. 10.0.1.5, 192.168.0.0/16). Port: single, list, or range (e.g. 443,6881:6889). Domain: hostnames or wildcards (e.g. *.youtube.com) — matches SOCKS5 clients that send a hostname; transparent traffic carries only an IP.'));
		o.rmempty = false;

		o = s.option(form.ListValue, 'wan', _('Target WAN'));
		uci.sections('gowan', 'wan').forEach(function(section) {
			o.value(section['.name'], section.label || section['.name']);
		});

		return m.render();
	}
});
