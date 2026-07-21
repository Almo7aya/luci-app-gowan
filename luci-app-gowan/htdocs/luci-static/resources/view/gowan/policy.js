'use strict';
'require view';
'require form';
'require uci';
'require ui';

return view.extend({
	load: function() {
		return uci.load('gowan');
	},

	render: function() {
		var m, s, o;

		ui.addNotification(null,
			E('p', _('Policy rules are stored but not yet enforced in this release — the policy engine ships with the next major version. Only the client IP rule type is planned for early enforcement.')),
			'warning');

		m = new form.Map('gowan', _('Policy Rules'),
			_('Pin traffic to a specific WAN backend. Rules are evaluated in order; the first match wins, everything else is load-balanced normally.'));

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
		o.placeholder = _('e.g. My laptop via WAN 1');

		o = s.option(form.ListValue, 'type', _('Type'));
		o.value('client_ip', _('Client IP'));
		o.default = 'client_ip';

		o = s.option(form.Value, 'match', _('Match'),
			_('Client IPv4 address or CIDR subnet'));
		o.datatype = 'or(ip4addr("nomask"), cidr4)';
		o.rmempty = false;

		o = s.option(form.ListValue, 'wan', _('Target WAN'));
		uci.sections('gowan', 'wan').forEach(function(section) {
			o.value(section['.name'], section.label || section['.name']);
		});

		return m.render();
	}
});
