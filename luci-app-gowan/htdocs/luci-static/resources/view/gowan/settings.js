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

		o = s.option(form.Flag, 'log_connections', _('Log connections'),
			_('One syslog line per dispatched connection'));
		o.default = '1';

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

		return m.render();
	}
});
