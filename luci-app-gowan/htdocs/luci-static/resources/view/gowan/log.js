'use strict';
'require view';
'require rpc';
'require poll';
'require dom';

var callLog = rpc.declare({
	object: 'gowan',
	method: 'log',
	params: [ 'lines' ],
	expect: { log: [] }
});

return view.extend({
	load: function() {
		return callLog(100);
	},

	render: function(lines) {
		var logview = E('pre', {
			'style': 'max-height:70vh;overflow-y:auto;white-space:pre-wrap;font-size:12px'
		});

		var update = function(entries) {
			logview.textContent = (entries && entries.length)
				? entries.join('\n')
				: _('No gowan log entries yet.');
			logview.scrollTop = logview.scrollHeight;
		};

		update(lines);

		poll.add(function() {
			return callLog(100).then(update);
		}, 10);

		return E('div', {}, [
			E('h2', {}, _('GoWAN Log')),
			E('p', { 'class': 'cbi-value-description' },
				_('Last 100 syslog entries tagged "gowan": daemon lifecycle, health flips, and one line per dispatched connection when connection logging is enabled.')),
			logview
		]);
	},

	handleSave: null,
	handleSaveApply: null,
	handleReset: null
});
