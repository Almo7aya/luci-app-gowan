'use strict';
'require view';
'require rpc';
'require uci';
'require dom';
'require ui';

var callSpeedtest = rpc.declare({
	object: 'gowan',
	method: 'speedtest',
	params: [ 'section' ]
});

function runTest(section, resultCell, btn) {
	btn.disabled = true;
	dom.content(resultCell, E('em', {}, _('Testing… (up to 20s)')));

	return callSpeedtest(section).then(function(res) {
		btn.disabled = false;
		if (!res || res.status !== 'ok') {
			var why = (res && res.status) ? res.status.replace(/_/g, ' ') : _('failed');
			dom.content(resultCell, E('span', { style: 'color:#dc2626' }, _('failed: %s').format(why)));
			return;
		}
		dom.content(resultCell, E('span', {}, [
			E('strong', {}, '%.2f Mbit/s'.format(res.mbps)),
			res.latency_ms > 0 ? '  ·  %d ms'.format(res.latency_ms) : ''
		]));
	}).catch(function() {
		btn.disabled = false;
		dom.content(resultCell, E('span', { style: 'color:#dc2626' }, _('request error')));
	});
}

return view.extend({
	load: function() { return uci.load('gowan'); },

	render: function() {
		var wans = uci.sections('gowan', 'wan');

		var table = E('table', { class: 'table' }, [
			E('tr', { class: 'tr table-titles' }, [
				E('th', { class: 'th' }, _('Backend')),
				E('th', { class: 'th' }, _('Interface')),
				E('th', { class: 'th' }, _('Result')),
				E('th', { class: 'th' }, _('Action'))
			])
		]);

		if (!wans.length) {
			table.appendChild(E('tr', { class: 'tr placeholder' }, [
				E('td', { class: 'td', colspan: 4 }, _('No WAN backends configured.'))
			]));
		}

		var rows = [];
		wans.forEach(function(w) {
			var section = w['.name'];
			var resultCell = E('td', { class: 'td' }, '–');
			var btn = E('button', { class: 'btn cbi-button cbi-button-action' }, _('Test'));
			btn.addEventListener('click', function() { runTest(section, resultCell, btn); });
			rows.push({ section: section, btn: btn });
			table.appendChild(E('tr', { class: 'tr' }, [
				E('td', { class: 'td' }, w.label || section),
				E('td', { class: 'td' }, w.interface || '-'),
				resultCell,
				E('td', { class: 'td' }, btn)
			]));
		});

		var testAll = E('button', { class: 'btn cbi-button cbi-button-action important' }, _('Test all'));
		testAll.addEventListener('click', function() {
			rows.reduce(function(chain, r) {
				return chain.then(function() { return r.btn.click(); });
			}, Promise.resolve());
		});

		return E('div', {}, [
			E('h2', {}, _('Per-WAN Speed Test')),
			E('p', { class: 'cbi-value-description' },
				_('Downloads a test file bound to each WAN interface and measures throughput and latency. Requires curl on the router.')),
			wans.length ? E('p', {}, testAll) : '',
			table
		]);
	},

	handleSave: null,
	handleSaveApply: null,
	handleReset: null
});
