'use strict';
'require view';
'require rpc';
'require poll';
'require dom';

var callUsage = rpc.declare({ object: 'gowan', method: 'usage', expect: { wans: [] } });

function fmtBytes(bytes) {
	var n = parseInt(bytes, 10) || 0, u = ['B', 'KiB', 'MiB', 'GiB', 'TiB'], i = 0;
	while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
	return (i === 0 ? String(n) : n.toFixed(2)) + ' ' + u[i];
}

function cell(rx, tx) {
	var total = (parseInt(rx, 10) || 0) + (parseInt(tx, 10) || 0);
	return E('td', { class: 'td' }, [
		E('strong', {}, fmtBytes(total)),
		E('br'),
		E('span', { style: 'color:#9ca3af;font-size:90%' },
			'↓ ' + fmtBytes(rx) + '  ↑ ' + fmtBytes(tx))
	]);
}

function renderTable(wans) {
	var table = E('table', { class: 'table' }, [
		E('tr', { class: 'tr table-titles' }, [
			E('th', { class: 'th' }, _('Backend')),
			E('th', { class: 'th' }, _('Interface')),
			E('th', { class: 'th' }, _('Today')),
			E('th', { class: 'th' }, _('This month')),
			E('th', { class: 'th' }, _('All time'))
		])
	]);

	if (!wans.length) {
		table.appendChild(E('tr', { class: 'tr placeholder' }, [
			E('td', { class: 'td', colspan: 5 }, _('No usage data yet.'))
		]));
		return table;
	}

	wans.forEach(function(w) {
		table.appendChild(E('tr', { class: 'tr' }, [
			E('td', { class: 'td' }, w.label || w.section),
			E('td', { class: 'td' }, w.device || '-'),
			cell(w.day_rx, w.day_tx),
			cell(w.month_rx, w.month_tx),
			cell(w.total_rx, w.total_tx)
		]));
	});
	return table;
}

return view.extend({
	load: function() { return callUsage(); },

	render: function(wans) {
		var container = E('div', {}, [
			E('h2', {}, _('Data Usage')),
			E('p', { class: 'cbi-value-description' },
				_('Cumulative whole-interface data per WAN — useful for metered links. Totals survive reboots; "This month" resets on the 1st.')),
			E('div', { id: 'gowan-usage-table' }, renderTable(wans))
		]);

		poll.add(function() {
			return callUsage().then(function(w) {
				dom.content(container.querySelector('#gowan-usage-table'), renderTable(w));
			});
		}, 10);

		return container;
	},

	handleSave: null,
	handleSaveApply: null,
	handleReset: null
});
