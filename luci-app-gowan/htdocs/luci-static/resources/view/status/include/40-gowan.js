'use strict';
'require baseclass';
'require rpc';

// GoWAN summary card on the main LuCI status dashboard
// (Status → Overview). Auto-included from view/status/include/.
// The status-include contract is load() -> data, then render(data)
// returning a Node synchronously (returning a Promise from render()
// shows "[object Promise]").

var callStatus = rpc.declare({ object: 'gowan', method: 'status' });

function dot(color) {
	return E('span', {
		style: 'display:inline-block;width:9px;height:9px;border-radius:50%;margin-right:6px;vertical-align:middle;background:' + color
	});
}

return baseclass.extend({
	title: _('GoWAN Multi-WAN'),

	load: function() {
		return L.resolveDefault(callStatus(), null);
	},

	render: function(s) {
		if (!s)
			return E('p', {}, E('em', {}, _('GoWAN service not responding.')));

		if (!s.enabled)
			return E('p', {}, [ dot('#9ca3af'), _('Disabled.') ]);

		var wans = s.wans || [];
		var up = wans.filter(function(w) { return w.status === 'up'; }).length;
		var active = wans.reduce(function(a, w) { return a + (w.active_connections || 0); }, 0);
		var total = wans.reduce(function(a, w) { return a + (w.total_connections || 0); }, 0);

		var rows = wans.map(function(w) {
			var color = w.status === 'up' ? '#16a34a' : (w.status === 'down' ? '#dc2626' : '#9ca3af');
			return E('tr', { class: 'tr' }, [
				E('td', { class: 'td', width: '33%' }, [ dot(color), (w.label || w.section) ]),
				E('td', { class: 'td' }, w.ip || _('no address')),
				E('td', { class: 'td', width: '20%' }, String(w.active_connections || 0))
			]);
		});

		var head = s.running
			? E('p', {}, [ dot('#16a34a'),
				_('Proxy online — %d/%d WANs up, %d active / %d total connections')
					.format(up, wans.length, active, total) ])
			: E('p', {}, [ dot('#dc2626'), _('Proxy not running.') ]);

		return E('div', {}, [
			head,
			E('table', { class: 'table' }, [
				E('tr', { class: 'tr table-titles' }, [
					E('th', { class: 'th' }, _('WAN')),
					E('th', { class: 'th' }, _('Source IP')),
					E('th', { class: 'th' }, _('Active'))
				])
			].concat(rows.length ? rows : [
				E('tr', { class: 'tr placeholder' }, E('td', { class: 'td', colspan: 3 }, _('No WAN backends.')))
			]))
		]);
	}
});
