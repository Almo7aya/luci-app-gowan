'use strict';
'require view';
'require rpc';
'require uci';
'require poll';
'require dom';

var callStart = rpc.declare({
	object: 'gowan',
	method: 'speedtest_start',
	params: [ 'section' ]
});

var callStatus = rpc.declare({
	object: 'gowan',
	method: 'speedtest_status',
	expect: { results: {} }
});

// Byte rate: B/s, KiB/s, MiB/s, GiB/s.
function fmtRate(bytesPerSec) {
	var n = bytesPerSec || 0, u = ['B/s', 'KiB/s', 'MiB/s', 'GiB/s'], i = 0;
	while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
	return (i === 0 ? String(Math.round(n)) : n.toFixed(2)) + ' ' + u[i];
}

function ago(ts) {
	var t = parseInt(ts, 10) || 0;
	if (t <= 0) return '';
	var s = Math.max(0, Math.floor(Date.now() / 1000) - t);
	if (s < 60) return _('%ds ago').format(s);
	if (s < 3600) return _('%dm ago').format(Math.floor(s / 60));
	return _('%dh ago').format(Math.floor(s / 3600));
}

// Renders one section's result cell from its server-side state.
function renderState(cell, st) {
	if (!st) { dom.content(cell, '–'); return; }

	if (st.running) {
		dom.content(cell, E('span', {}, [
			E('span', { class: 'spinning' }),
			' ', E('em', {}, _('Testing… (up to 20s)'))
		]));
		return;
	}
	if (!st.status) { dom.content(cell, '–'); return; }
	if (st.status !== 'ok') {
		dom.content(cell, E('span', { style: 'color:#dc2626' },
			_('failed: %s').format(String(st.status).replace(/_/g, ' '))));
		return;
	}
	dom.content(cell, E('span', {}, [
		E('strong', {}, fmtRate(st.bytes_per_sec)),
		(st.latency_ms > 0 ? '  ·  %d ms'.format(st.latency_ms) : ''),
		(st.ts ? E('span', { style: 'color:#9ca3af' }, '  (' + ago(st.ts) + ')') : '')
	]));
}

return view.extend({
	load: function() {
		return Promise.all([ uci.load('gowan'), callStatus() ]);
	},

	render: function(data) {
		var wans = uci.sections('gowan', 'wan');
		var cells = {};   // section -> result <td>
		var btns = {};    // section -> button

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

		wans.forEach(function(w) {
			var section = w['.name'];
			var cell = E('td', { class: 'td' }, '–');
			var btn = E('button', { class: 'btn cbi-button cbi-button-action' }, _('Test'));
			btn.addEventListener('click', function() {
				callStart(section).then(function() {
					renderState(cell, { running: true });
				});
			});
			cells[section] = cell;
			btns[section] = btn;
			table.appendChild(E('tr', { class: 'tr' }, [
				E('td', { class: 'td' }, w.label || section),
				E('td', { class: 'td' }, w.interface || '-'),
				cell,
				E('td', { class: 'td' }, btn)
			]));
		});

		var testAll = E('button', { class: 'btn cbi-button cbi-button-action important' }, _('Test all'));
		testAll.addEventListener('click', function() {
			wans.forEach(function(w) {
				callStart(w['.name']).then(function() {
					renderState(cells[w['.name']], { running: true });
				});
			});
		});

		var applyStates = function(results) {
			results = results || {};
			Object.keys(cells).forEach(function(section) {
				var st = results[section];
				renderState(cells[section], st);
				if (btns[section]) btns[section].disabled = !!(st && st.running);
			});
		};

		applyStates(data[1]);

		// Server-side state → running tests survive reloads and tab
		// changes; the page just reflects whatever the router reports.
		poll.add(function() {
			return callStatus().then(applyStates);
		}, 2);

		return E('div', {}, [
			E('h2', {}, _('Per-WAN Speed Test')),
			E('p', { class: 'cbi-value-description' },
				_('Downloads a test file bound to each WAN interface and measures throughput (bytes/s) and latency. Tests run on the router and keep going even if you leave this page. Requires curl on the router.')),
			wans.length ? E('p', {}, testAll) : '',
			table
		]);
	},

	handleSave: null,
	handleSaveApply: null,
	handleReset: null
});
