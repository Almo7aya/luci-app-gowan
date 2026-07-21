'use strict';
'require view';
'require form';

return view.extend({
	render: function() {
		var m, s, o;

		m = new form.Map('gowan', _('Access Control'),
			_('Rules guard the SOCKS5 port on the router input chain and are evaluated in order; the first match wins, then the default verdict from Settings applies. Changes take effect on Save & Apply.'));

		s = m.section(form.GridSection, 'acl');
		s.addremove = true;
		s.anonymous = true;
		s.sortable = true;
		s.nodescriptions = true;
		s.addbtntitle = _('Add rule');

		o = s.option(form.Flag, 'enabled', _('Enabled'));
		o.default = '1';
		o.rmempty = false;

		o = s.option(form.ListValue, 'verdict', _('Verdict'));
		o.value('allow', _('Allow'));
		o.value('deny', _('Deny'));
		o.default = 'allow';

		o = s.option(form.DynamicList, 'subnet', _('Source subnets'),
			_('Client IPv4 addresses or CIDR subnets this rule matches'));
		o.datatype = 'or(ip4addr("nomask"), cidr4)';
		o.placeholder = '10.0.1.0/24';
		o.rmempty = false;

		o = s.option(form.Value, 'description', _('Description'));
		o.placeholder = _('e.g. Guest WiFi');

		return m.render();
	}
});
