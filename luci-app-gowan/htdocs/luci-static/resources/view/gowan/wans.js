'use strict';
'require view';
'require form';
'require tools.widgets as widgets';

return view.extend({
	render: function() {
		var m, s, o;

		m = new form.Map('gowan', _('WAN Backends'),
			_('Each backend references an OpenWrt logical interface. Its current IPv4 address is resolved when the service starts; interface up/down events reload the service automatically.'));

		s = m.section(form.GridSection, 'wan');
		s.addremove = true;
		s.anonymous = false;
		s.nodescriptions = true;
		s.addbtntitle = _('Add WAN backend');

		o = s.option(form.Flag, 'enabled', _('Enabled'));
		o.default = '1';
		o.rmempty = false;

		o = s.option(form.Value, 'label', _('Label'),
			_('Human-readable name shown on the overview page'));
		o.placeholder = _('e.g. 4G Router A');

		o = s.option(widgets.NetworkSelect, 'interface', _('Interface'),
			_('OpenWrt logical interface this backend egresses through'));
		o.exclude = 'loopback';
		o.nocreate = true;
		o.rmempty = false;

		o = s.option(form.Value, 'ratio', _('Contention ratio'),
			_('Round-robin weight: a backend with ratio 2 receives twice the connections of one with ratio 1'));
		o.datatype = 'range(1,100)';
		o.default = '1';
		o.rmempty = false;

		o = s.option(form.Value, 'metric', _('Metric (tier)'),
			_('Lower is preferred. WANs with the same metric load-balance together; a higher-metric WAN is a backup, used only when every lower-metric WAN is down. Leave 0 to keep all WANs active.'));
		o.datatype = 'range(0,1000)';
		o.default = '0';

		// Per-WAN health check overrides — edit dialog only. Empty
		// fields inherit the global settings.
		o = s.option(form.ListValue, 'check_type', _('Check type override'),
			_('Leave "inherit" to use the global health-check settings'));
		o.modalonly = true;
		o.value('', _('inherit'));
		o.value('tcp', _('TCP connect'));
		o.value('http', _('HTTP GET'));
		o.value('none', _('Disabled'));

		o = s.option(form.Value, 'check_target', _('Check target override'));
		o.modalonly = true;
		o.placeholder = _('inherit global');

		o = s.option(form.Value, 'check_interval', _('Check interval override (s)'));
		o.modalonly = true;
		o.datatype = 'range(1,3600)';
		o.placeholder = _('inherit');

		o = s.option(form.Value, 'check_timeout', _('Check timeout override (s)'));
		o.modalonly = true;
		o.datatype = 'range(1,60)';
		o.placeholder = _('inherit');

		o = s.option(form.Value, 'check_fail_threshold', _('Fail threshold override'));
		o.modalonly = true;
		o.datatype = 'range(1,20)';
		o.placeholder = _('inherit');

		o = s.option(form.Value, 'check_rise_threshold', _('Rise threshold override'));
		o.modalonly = true;
		o.datatype = 'range(1,20)';
		o.placeholder = _('inherit');

		return m.render();
	}
});
