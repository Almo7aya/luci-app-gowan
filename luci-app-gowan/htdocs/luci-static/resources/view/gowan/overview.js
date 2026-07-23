'use strict';
'require view';
'require rpc';
'require uci';
'require poll';
'require dom';

var callStatus = rpc.declare({ object: 'gowan', method: 'status' });
var callStats = rpc.declare({ object: 'gowan', method: 'stats' });

var HISTORY = 60;         // samples kept per series
var STORE_KEY = 'gowan.throughput.v1';
var PALETTE = ['#2563eb', '#16a34a', '#d97706', '#9333ea', '#dc2626', '#0891b2', '#ca8a04', '#4f46e5'];

var pollInterval = 3;     // seconds; overridden from UCI at render

// Per-device rolling state (bytes/sec), restored from localStorage so the
// graph survives a page reload.
var prevSample = {};      // device -> { rx, tx, t }
var rateHistory = {};     // device -> [{ down, up }]  (bytes/sec)

(function restoreHistory() {
	try {
		var saved = JSON.parse(localStorage.getItem(STORE_KEY) || 'null');
		if (saved && saved.rateHistory) rateHistory = saved.rateHistory;
		if (saved && saved.prevSample) prevSample = saved.prevSample;
	} catch (e) { /* private mode / quota / corrupt: start fresh */ }
})();

function persistHistory() {
	try {
		localStorage.setItem(STORE_KEY, JSON.stringify({ prevSample: prevSample, rateHistory: rateHistory }));
	} catch (e) { /* ignore */ }
}

// Byte rate, full precision: B/s, KiB/s, MiB/s, GiB/s.
function fmtRate(bytesPerSec) {
	var n = bytesPerSec || 0, u = ['B/s', 'KiB/s', 'MiB/s', 'GiB/s'], i = 0;
	while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
	return (i === 0 ? String(Math.round(n)) : n.toFixed(2)) + ' ' + u[i];
}

// Compact byte rate for axis labels (kept short so they never clip).
function fmtRateAxis(bytesPerSec) {
	var n = bytesPerSec || 0, u = ['B/s', 'KiB/s', 'MiB/s', 'GiB/s'], i = 0;
	while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
	var v = (i === 0 || n >= 100) ? Math.round(n) : n.toFixed(1);
	return v + ' ' + u[i];
}

function fmtSince(ts) {
	var t = parseInt(ts, 10) || 0;
	if (t <= 0) return '-';
	var s = Math.max(0, Math.floor(Date.now() / 1000) - t);
	if (s < 60) return s + 's';
	if (s < 3600) return Math.floor(s / 60) + 'm';
	if (s < 86400) return Math.floor(s / 3600) + 'h ' + Math.floor((s % 3600) / 60) + 'm';
	return Math.floor(s / 86400) + 'd ' + Math.floor((s % 86400) / 3600) + 'h';
}

function statusDot(status, enabled) {
	var color = '#9ca3af', label = _('unknown');
	if (!enabled) { color = '#9ca3af'; label = _('disabled'); }
	else if (status === 'up') { color = '#16a34a'; label = _('up'); }
	else if (status === 'down') { color = '#dc2626'; label = _('down'); }
	return E('span', {}, [
		E('span', { style: 'display:inline-block;width:10px;height:10px;border-radius:50%;margin-right:6px;vertical-align:middle;background:' + color }),
		label
	]);
}

// Folds a new stats sample into per-device rates + rolling history.
function ingestStats(stats) {
	var now = Date.now() / 1000;
	var rates = {};
	((stats && stats.wans) || []).forEach(function(w) {
		var dev = w.device;
		if (!dev) return;
		var prev = prevSample[dev];
		var cur = { rx: parseInt(w.rx_bytes, 10) || 0, tx: parseInt(w.tx_bytes, 10) || 0, t: now };
		var dt = prev ? now - prev.t : 0;
		// Only record when the gap looks like a normal poll interval; a large
		// gap (page was closed, restored from storage) would average out to a
		// misleading bar, so reseed instead of pushing it.
		if (prev && dt > 0 && dt < pollInterval * 4) {
			// Counters can reset (reboot/renew); clamp negatives to 0.
			var down = Math.max(0, cur.rx - prev.rx) / dt;   // bytes/sec
			var up = Math.max(0, cur.tx - prev.tx) / dt;
			rates[dev] = { down: down, up: up };
			var h = rateHistory[dev] || (rateHistory[dev] = []);
			h.push({ down: down, up: up });
			while (h.length > HISTORY) h.shift();
		}
		prevSample[dev] = cur;
	});
	persistHistory();
	return rates;
}

// Multi-series SVG line chart (no external libraries; CSP-safe). Scales
// uniformly with container width so axis labels stay crisp and unclipped.
function lineChart(series) {
	var W = 760, H = 200, padL = 70, padR = 12, padT = 12, padB = 16;
	var plotW = W - padL - padR, plotH = H - padT - padB;
	var max = 1;
	series.forEach(function(s) { s.points.forEach(function(v) { if (v > max) max = v; }); });

	var ns = 'http://www.w3.org/2000/svg';
	var svg = document.createElementNS(ns, 'svg');
	svg.setAttribute('viewBox', '0 0 ' + W + ' ' + H);
	svg.style.width = '100%';
	svg.style.height = 'auto';

	function mkline(x1, y1, x2, y2, dash) {
		var l = document.createElementNS(ns, 'line');
		l.setAttribute('x1', x1); l.setAttribute('y1', y1);
		l.setAttribute('x2', x2); l.setAttribute('y2', y2);
		l.setAttribute('stroke', '#9ca3af'); l.setAttribute('stroke-width', '1');
		l.setAttribute('opacity', '0.5');
		if (dash) l.setAttribute('stroke-dasharray', dash);
		return l;
	}
	function mktext(x, y, str) {
		var t = document.createElementNS(ns, 'text');
		t.setAttribute('x', x); t.setAttribute('y', y);
		t.setAttribute('font-size', '10'); t.setAttribute('fill', '#9ca3af');
		t.setAttribute('text-anchor', 'end');
		t.textContent = str;
		return t;
	}

	// Gridlines + y-axis scale labels (0, mid, max).
	[0, 0.5, 1].forEach(function(f) {
		var y = padT + plotH * (1 - f);
		svg.appendChild(mkline(padL, y, W - padR, y, '2 3'));
		svg.appendChild(mktext(padL - 8, y + 3, fmtRateAxis(max * f)));
	});

	series.forEach(function(s) {
		if (s.points.length < 2) return;
		var step = plotW / (HISTORY - 1);
		var offset = HISTORY - s.points.length;   // right-align newest sample
		var d = '';
		s.points.forEach(function(v, i) {
			var x = padL + (offset + i) * step;
			var y = padT + plotH * (1 - v / max);
			d += (i === 0 ? 'M' : 'L') + x.toFixed(1) + ',' + y.toFixed(1);
		});
		var path = document.createElementNS(ns, 'path');
		path.setAttribute('d', d);
		path.setAttribute('fill', 'none');
		path.setAttribute('stroke', s.color);
		path.setAttribute('stroke-width', '1.5');
		svg.appendChild(path);
	});

	return svg;
}

function renderChart(status) {
	var wans = (status && status.wans) || [];
	var series = [], legend = [];
	wans.forEach(function(w, idx) {
		var h = rateHistory[w.device] || [];
		var color = PALETTE[idx % PALETTE.length];
		// Chart total throughput (down+up) per WAN.
		series.push({ color: color, points: h.map(function(p) { return p.down + p.up; }) });
		legend.push(E('span', { style: 'margin-right:14px;white-space:nowrap' }, [
			E('span', { style: 'display:inline-block;width:12px;height:3px;vertical-align:middle;margin-right:5px;background:' + color }),
			(w.label || w.section)
		]));
	});

	var hasData = series.some(function(s) { return s.points.length >= 2; });
	return E('div', {}, [
		E('div', { style: 'margin:4px 0 8px' }, legend),
		hasData ? lineChart(series)
			: E('p', { class: 'cbi-value-description' }, _('Collecting throughput samples…'))
	]);
}

function renderTable(status, rates) {
	var table = E('table', { class: 'table' }, [
		E('tr', { class: 'tr table-titles' }, [
			E('th', { class: 'th' }, _('Backend')),
			E('th', { class: 'th' }, _('Interface')),
			E('th', { class: 'th' }, _('Source IP')),
			E('th', { class: 'th' }, _('Ratio')),
			E('th', { class: 'th' }, _('Status')),
			E('th', { class: 'th' }, _('For')),
			E('th', { class: 'th' }, _('↓ / ↑ now')),
			E('th', { class: 'th' }, _('Conns (act / total)')),
			E('th', { class: 'th' }, _('Checks OK / Fail'))
		])
	]);

	var wans = (status && status.wans) || [];
	if (!wans.length) {
		table.appendChild(E('tr', { class: 'tr placeholder' }, [
			E('td', { class: 'td', colspan: 9 }, _('No WAN backends configured. Add some under WAN Backends.'))
		]));
		return table;
	}

	wans.forEach(function(w) {
		var r = rates[w.device] || {};
		var devlabel = w.device ? '%s (%s)'.format(w.interface, w.device) : (w.interface || '-');
		table.appendChild(E('tr', { class: 'tr' }, [
			E('td', { class: 'td' }, w.label || w.section),
			E('td', { class: 'td' }, devlabel),
			E('td', { class: 'td' }, w.ip || _('no address')),
			E('td', { class: 'td' }, String(w.ratio || 1)),
			E('td', { class: 'td' }, statusDot(w.status, w.enabled)),
			E('td', { class: 'td' }, fmtSince(w.since)),
			E('td', { class: 'td' }, (r.down != null ? fmtRate(r.down) : '–') + ' / ' + (r.up != null ? fmtRate(r.up) : '–')),
			E('td', { class: 'td' }, '%d / %d'.format(w.active_connections || 0, w.total_connections || 0)),
			E('td', { class: 'td' }, '%d / %d'.format(w.checks_ok || 0, w.checks_failed || 0))
		]));
	});
	return table;
}

function sumConns(status, field) {
	return ((status && status.wans) || []).reduce(function(a, w) { return a + (w[field] || 0); }, 0);
}

return view.extend({
	load: function() {
		return Promise.all([ uci.load('gowan'), callStatus(), callStats() ]);
	},

	render: function(data) {
		pollInterval = parseInt(uci.get('gowan', 'main', 'stats_interval'), 10) || 3;
		ingestStats(data[2]);

		var container = E('div', {}, [
			E('h2', {}, _('GoWAN')),
			E('div', { id: 'gowan-proxy-state' }),
			E('h3', {}, _('Live Throughput')),
			E('div', { id: 'gowan-chart' }),
			E('h3', {}, _('WAN Backends')),
			E('div', { id: 'gowan-wan-table' }),
			E('p', { class: 'cbi-value-description' },
				_('Throughput is derived from whole-interface /proc/net/dev counters, not proxy-only traffic.'))
		]);

		var update = function(status, stats) {
			var rates = ingestStats(stats);
			var state;
			if (!status || !status.enabled)
				state = E('p', {}, [statusDot('down', true), ' ', _('GoWAN is disabled. Enable it under Settings.')]);
			else if (status.running)
				state = E('p', {}, [statusDot('up', true), ' ',
					_('Proxy running — %d active / %d total connections').format(
						sumConns(status, 'active_connections'), sumConns(status, 'total_connections'))]);
			else
				state = E('p', {}, [statusDot('down', true), ' ', _('Proxy is NOT running (enabled in config).')]);

			dom.content(container.querySelector('#gowan-proxy-state'), state);
			dom.content(container.querySelector('#gowan-chart'), renderChart(status));
			dom.content(container.querySelector('#gowan-wan-table'), renderTable(status, rates));
		};

		update(data[1], data[2]);
		poll.add(function() {
			return Promise.all([callStatus(), callStats()]).then(function(res) { update(res[0], res[1]); });
		}, pollInterval);

		return container;
	},

	handleSave: null,
	handleSaveApply: null,
	handleReset: null
});
