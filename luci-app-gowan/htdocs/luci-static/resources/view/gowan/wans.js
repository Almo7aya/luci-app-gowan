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

		return m.render();
	}
});
