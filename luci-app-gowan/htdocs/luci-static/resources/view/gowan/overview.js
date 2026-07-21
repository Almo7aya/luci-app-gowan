'use strict';
'require view';
'require rpc';
'require poll';
'require dom';

var callStatus = rpc.declare({
	object: 'gowan',
	method: 'status'
});

var callStats = rpc.declare({
	object: 'gowan',
	method: 'stats'
});

function formatBytes(bytes) {
	var n = parseInt(bytes, 10) || 0;
	var units = [ 'B', 'KiB', 'MiB', 'GiB', 'TiB' ];
	var i = 0;
	while (n >= 1024 && i < units.length - 1) {
		n /= 1024;
		i++;
	}
	return (i === 0 ? String(n) : n.toFixed(1)) + ' ' + units[i];
}

function formatSince(ts) {
	var t = parseInt(ts, 10) || 0;
	if (t <= 0)
		return '-';
	var secs = Math.max(0, Math.floor(Date.now() / 1000) - t);
	if (secs < 60)
		return _('%ds').format(secs);
	if (secs < 3600)
		return _('%dm').format(Math.floor(secs / 60));
	if (secs < 86400)
		return _('%dh %dm').format(Math.floor(secs / 3600), Math.floor((secs % 3600) / 60));
	return _('%dd %dh').format(Math.floor(secs / 86400), Math.floor((secs % 86400) / 3600));
}

function statusDot(status, enabled) {
	var color = '#ccc', label = _('unknown');

	if (!enabled) {
		color = '#999';
		label = _('disabled');
	}
	else if (status === 'up') {
		color = '#2ea44f';
		label = _('up');
	}
	else if (status === 'down') {
		color = '#d73a49';
		label = _('down');
	}

	return E('span', {}, [
		E('span', {
			'style': 'display:inline-block;width:10px;height:10px;border-radius:50%;margin-right:6px;background:' + color
		}),
		label
	]);
}

function renderWans(status, stats) {
	var statsBySection = {};

	((stats && stats.wans) || []).forEach(function(w) {
		statsBySection[w.section] = w;
	});

	var table = E('table', { 'class': 'table' }, [
		E('tr', { 'class': 'tr table-titles' }, [
			E('th', { 'class': 'th' }, _('Backend')),
			E('th', { 'class': 'th' }, _('Interface')),
			E('th', { 'class': 'th' }, _('Source IP')),
			E('th', { 'class': 'th' }, _('Ratio')),
			E('th', { 'class': 'th' }, _('Status')),
			E('th', { 'class': 'th' }, _('For')),
			E('th', { 'class': 'th' }, _('Checks OK / Failed')),
			E('th', { 'class': 'th' }, _('Conns (active / total)')),
			E('th', { 'class': 'th' }, _('RX / TX (interface)'))
		])
	]);

	var wans = (status && status.wans) || [];

	if (!wans.length) {
		table.appendChild(E('tr', { 'class': 'tr placeholder' }, [
			E('td', { 'class': 'td', 'colspan': 9 },
				_('No WAN backends configured. Add some under WAN Backends.'))
		]));
		return table;
	}

	wans.forEach(function(w) {
		var st = statsBySection[w.section] || {};
		var devlabel = w.device ? '%s (%s)'.format(w.interface, w.device) : (w.interface || '-');

		table.appendChild(E('tr', { 'class': 'tr' }, [
			E('td', { 'class': 'td' }, w.label || w.section),
			E('td', { 'class': 'td' }, devlabel),
			E('td', { 'class': 'td' }, w.ip || _('no address')),
			E('td', { 'class': 'td' }, String(w.ratio || 1)),
			E('td', { 'class': 'td' }, statusDot(w.status, w.enabled)),
			E('td', { 'class': 'td' }, formatSince(w.since)),
			E('td', { 'class': 'td' }, '%d / %d'.format(w.checks_ok || 0, w.checks_failed || 0)),
			E('td', { 'class': 'td' }, '%d / %d'.format(w.active_connections || 0, w.total_connections || 0)),
			E('td', { 'class': 'td' },
				formatBytes(st.rx_bytes) + ' / ' + formatBytes(st.tx_bytes))
		]));
	});

	return table;
}

return view.extend({
	load: function() {
		return Promise.all([ callStatus(), callStats() ]);
	},

	render: function(data) {
		var container = E('div', {}, [
			E('h2', {}, _('GoWAN')),
			E('div', { 'id': 'gowan-proxy-state' }),
			E('h3', {}, _('WAN Backends')),
			E('div', { 'id': 'gowan-wan-table' }),
			E('p', { 'class': 'cbi-value-description' },
				_('RX/TX are whole-interface counters from /proc/net/dev, not proxy-only traffic.'))
		]);

		var update = function(status, stats) {
			var state;

			if (!status || !status.enabled)
				state = E('p', {}, [ statusDot('down', true), ' ',
					_('GoWAN is disabled. Enable it under Settings.') ]);
			else if (status.running)
				state = E('p', {}, [ statusDot('up', true), ' ',
					_('Proxy running, listening on %s').format(status.listen) ]);
			else
				state = E('p', {}, [ statusDot('down', true), ' ',
					_('Proxy is NOT running (enabled in config).') ]);

			dom.content(container.querySelector('#gowan-proxy-state'), state);
			dom.content(container.querySelector('#gowan-wan-table'), renderWans(status, stats));
		};

		update(data[0], data[1]);

		poll.add(function() {
			return Promise.all([ callStatus(), callStats() ]).then(function(res) {
				update(res[0], res[1]);
			});
		}, 5);

		return container;
	},

	handleSave: null,
	handleSaveApply: null,
	handleReset: null
});
