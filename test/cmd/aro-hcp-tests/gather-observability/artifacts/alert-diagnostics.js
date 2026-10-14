(function () {
  'use strict';
  const report = JSON.parse(document.getElementById('alert-diagnostics-data').textContent);
  const rangeStart = Date.parse(report.start), rangeEnd = Date.parse(report.end);
  const panes = Array.from(document.querySelectorAll('.metric-history'));
  const charts = new Map();
  let library;

  function text(parent, tag, value, className) {
    const node = document.createElement(tag);
    node.className = className || 'diagnostics-note';
    node.textContent = value;
    parent.appendChild(node);
    return node;
  }

  function loadCharts() {
    if (window.echarts) return Promise.resolve();
    if (!library) library = new Promise((resolve, reject) => {
      const script = document.createElement('script');
      script.src = 'https://go-echarts.github.io/go-echarts-assets/assets/echarts.min.js';
      script.onload = resolve;
      script.onerror = () => reject(new Error('Chart CDN unavailable. Expressions and diagnostic messages remain available.'));
      document.head.appendChild(script);
    });
    return library;
  }

  function stepMilliseconds(step) {
    if (/^\d+(?:\.\d+)?$/.test(step)) return Number(step) * 1000;
    const units = {ms: 1, s: 1000, m: 60000, h: 3600000, d: 86400000, w: 604800000, y: 31536000000};
    let total = 0;
    const rest = String(step || '').replace(/(\d+(?:\.\d+)?)(ms|s|m|h|d|w|y)/g, (_, value, unit) => {
      total += Number(value) * units[unit];
      return '';
    });
    return rest ? 0 : total;
  }

  function samples(values, step) {
    const sorted = (values || []).filter(pair => typeof pair[0] === 'number' && Number.isFinite(pair[0]))
      .map(pair => {
        const value = pair[1] === null || String(pair[1]).trim() === '' ? NaN : Number(pair[1]);
        return [pair[0] * 1000, Number.isFinite(value) ? value : null];
      }).sort((a, b) => a[0] - b[0]);
    const result = [];
    sorted.forEach(pair => {
      const previous = result[result.length - 1];
      // One null interrupts the line without allocating an entire time grid.
      if (previous && step > 0 && pair[0] - previous[0] > step * 1.5) result.push([previous[0] + step, null]);
      result.push(pair);
    });
    return result;
  }

  function recordedInterval(pane, body) {
    const start = Date.parse(pane.dataset.start);
    let end = Date.parse(pane.dataset.end);
    if (!Number.isFinite(start)) {
      text(body, 'p', 'Recorded firing interval unavailable: start time is unknown; no shading.', 'diagnostics-note diagnostics-warning');
      return null;
    }
    if (!Number.isFinite(end)) {
      if (pane.dataset.condition.toLowerCase() !== 'fired') {
        text(body, 'p', 'Recorded firing interval unavailable: resolved or unknown condition has no end time; no shading.', 'diagnostics-note diagnostics-warning');
        return null;
      }
      end = rangeEnd;
      text(body, 'p', 'Unresolved alert: recorded firing shading extends to report end (' + report.end + ').', 'diagnostics-note diagnostics-warning');
    }
    if (end < start) {
      text(body, 'p', 'Recorded firing interval unavailable: end precedes start; no shading.', 'diagnostics-note diagnostics-warning');
      return null;
    }
    const clipped = [Math.max(start, rangeStart), Math.min(end, rangeEnd)];
    if (clipped[0] >= clipped[1]) {
      text(body, 'p', 'Recorded firing interval has no duration inside the report range; no shading.', 'diagnostics-note diagnostics-warning');
      return null;
    }
    text(body, 'p', 'Recorded firing for this alert instance: ' + new Date(clipped[0]).toISOString() + ' to ' + new Date(clipped[1]).toISOString() + '. Shading is from recorded alert times, not inferred from samples. Series are not matched to this alert\'s labels; shading does not mean all series fired.');
    return clipped;
  }

  function visible(pane) {
    if (!pane.open || pane.closest('.alert-row').style.display === 'none') return false;
    for (let parent = pane.parentElement; parent; parent = parent.parentElement) {
      if (parent.tagName === 'DETAILS' && !parent.open) return false;
    }
    const rect = pane.getBoundingClientRect();
    return rect.width > 0 && rect.height > 0 && rect.bottom >= 0 && rect.top <= window.innerHeight;
  }

  function initialize(pane) {
    if (!visible(pane)) return;
    if (pane.dataset.initialized) {
      pane.querySelectorAll('.diagnostics-chart').forEach(host => charts.get(host)?.resize());
      return;
    }
    pane.dataset.initialized = 'true';
    const body = pane.querySelector('.diagnostics-body');
    if (report.error) {
      text(body, 'p', report.error, 'diagnostics-note diagnostics-error');
      return;
    }
    const entry = (report.alerts || [])[Number(pane.closest('.alert-row').dataset.alertIdx)];
    if (!entry) {
      text(body, 'p', 'Diagnostics unavailable for this alert.', 'diagnostics-note diagnostics-error');
      return;
    }
    if (entry.error) text(body, 'p', 'Diagnostics error: ' + entry.error, 'diagnostics-note diagnostics-error');
    if (entry.warning) text(body, 'p', 'Warning: ' + entry.warning, 'diagnostics-note diagnostics-warning');
    if (report.schemaVersion !== 1 || !Number.isFinite(rangeStart) || !Number.isFinite(rangeEnd) || rangeEnd < rangeStart) {
      text(body, 'p', 'Diagnostics report has an unsupported schema or invalid time range.', 'diagnostics-note diagnostics-error');
      return;
    }
    text(body, 'p', 'UTC range: ' + report.start + ' to ' + report.end + '. Collected: ' + report.generatedAt + '. Static data; no live queries.');
    const interval = recordedInterval(pane, body);
    if (!entry.charts?.length && !entry.error) text(body, 'p', 'No diagnostic conditions available.');
    (entry.charts || []).forEach((condition, conditionIndex) => {
      const section = text(body, 'section', '', 'diagnostics-condition');
      text(section, 'h4', 'Condition ' + (conditionIndex + 1) + ' | Path: ' + condition.path);
      text(section, 'pre', condition.expression, 'alert-expr');
      if (condition.fallback) text(section, 'p', 'Fallback: ' + condition.fallback, 'diagnostics-note diagnostics-warning');
      const series = [];
      let finite = false;
      (condition.queries || []).forEach((reference, queryIndex) => {
        const query = (report.queries || [])[reference.result];
        if (!query) {
          text(section, 'p', 'Query result unavailable (' + reference.role + ').', 'diagnostics-note diagnostics-error');
          return;
        }
        text(section, 'p', reference.role + ' | Workspace: ' + query.workspace + ' | Step: ' + query.step + ' | ' + (reference.role === 'right' ? 'dashed line' : 'solid line'));
        text(section, 'pre', query.expression, 'alert-expr');
        if (query.error) text(section, 'p', 'Query error: ' + query.error, 'diagnostics-note diagnostics-error');
        (query.warnings || []).forEach(warning => text(section, 'p', 'Query warning: ' + warning, 'diagnostics-note diagnostics-warning'));
        const step = stepMilliseconds(query.step);
        if (!step) text(section, 'p', 'Query step unavailable: showing isolated samples to avoid bridging missing data.', 'diagnostics-note diagnostics-warning');
        let queryFinite = false;
        (query.series || []).forEach((item, seriesIndex) => {
          const labels = Object.keys(item.metric || {}).sort().map(key => key + '=' + JSON.stringify(item.metric[key])).join(', ');
          const data = samples(item.values, step);
          queryFinite ||= data.some(pair => pair[1] !== null && pair[0] >= rangeStart && pair[0] <= rangeEnd);
          series.push({
            name: reference.role + ' ' + (queryIndex + 1) + '.' + (seriesIndex + 1) + ' {' + labels + '}',
            type: 'line', data: data, connectNulls: false, showSymbol: true, showAllSymbol: true,
            symbolSize: (value, params) => {
              if (value[1] === null) return 0;
              if (!step) return 5;
              const connected = index => data[index] && data[index][1] !== null && data[index][0] >= rangeStart && data[index][0] <= rangeEnd;
              return connected(params.dataIndex - 1) || connected(params.dataIndex + 1) ? 0 : 5;
            },
            lineStyle: {type: reference.role === 'right' ? 'dashed' : 'solid', width: step ? 1.5 : 0},
            emphasis: {focus: 'series'}
          });
        });
        finite ||= queryFinite;
        if (!queryFinite && !query.error) text(section, 'p', 'No data: no finite samples in this query\'s report range.');
      });
      const thresholds = (condition.thresholds || []).filter(threshold => typeof threshold.value === 'number' && Number.isFinite(threshold.value));
      thresholds.forEach(threshold => text(section, 'p', 'Scalar threshold: ' + threshold.operator + ' ' + threshold.value + ' (dashed line)'));
      if (!finite) {
        text(section, 'p', 'No chart data: no finite samples in the report range. Missing samples are not zero.');
        return;
      }
      // A separate overlay keeps recorded times and thresholds independent of legend selection.
      series.push({name: 'Recorded firing / thresholds', type: 'line', data: [], silent: true,
        markArea: interval ? {silent: true, itemStyle: {color: 'rgba(248,81,73,0.12)'}, label: {show: false}, data: [[{name: 'Recorded firing for this alert instance', xAxis: interval[0]}, {xAxis: interval[1]}]]} : undefined,
        markLine: {silent: true, symbol: 'none', lineStyle: {type: 'dashed', color: '#d29922'}, label: {position: 'insideEndTop', formatter: params => params.name}, data: thresholds.map(threshold => ({name: threshold.operator + ' ' + threshold.value, yAxis: threshold.value}))}
      });
      const host = text(section, 'div', '', 'diagnostics-chart');
      host.setAttribute('role', 'img');
      host.setAttribute('aria-label', 'Metric history for condition ' + (conditionIndex + 1) + ', UTC; recorded firing shading is for this alert instance only');
      const status = text(section, 'p', 'Loading chart library...');
      // markLine does not contribute to ECharts' data extent. Include thresholds
      // explicitly, with room for their labels even when the extent is constant.
      const yBounds = extent => {
        const values = [extent.min, extent.max, ...thresholds.map(threshold => threshold.value)].filter(Number.isFinite);
        if (!values.length) return [0, 1];
        const min = Math.min(...values), max = Math.max(...values);
        const padding = (max - min || Math.max(Math.abs(min), 1)) * 0.05;
        return [min - padding, max + padding];
      };
      const option = {
        backgroundColor: 'transparent', animation: false, useUTC: true,
        textStyle: {color: '#8b949e'},
        tooltip: {trigger: 'axis', renderMode: 'richText', confine: true},
        legend: {type: 'scroll', data: series.slice(0, -1).map(item => item.name), textStyle: {color: '#8b949e'}, pageTextStyle: {color: '#8b949e'}},
        grid: {left: 12, right: 20, top: 48, bottom: 80, containLabel: true},
        xAxis: {type: 'time', min: rangeStart, max: rangeEnd, name: 'UTC', nameLocation: 'middle', nameGap: 28, axisLabel: {hideOverlap: true}},
        yAxis: {type: 'value', scale: true, min: extent => yBounds(extent)[0], max: extent => yBounds(extent)[1], splitLine: {lineStyle: {color: '#30363d'}}},
        dataZoom: [{type: 'inside', filterMode: 'none'}, {type: 'slider', bottom: 4, filterMode: 'none'}],
        series: series
      };
      loadCharts().then(() => {
        status.remove();
        // The CDN may finish after this pane or an ancestor was collapsed.
        host._diagnosticsOption = option;
        draw(host);
      }).catch(error => { status.textContent = error.message; status.className = 'diagnostics-note diagnostics-error'; });
      if (resizeObserver) resizeObserver.observe(host);
    });
  }

  function draw(host) {
    if (!visible(host.closest('.metric-history')) || !host._diagnosticsOption) return;
    if (!charts.has(host)) {
      const chart = window.echarts.init(host, 'dark');
      chart.setOption(host._diagnosticsOption);
      charts.set(host, chart);
    } else charts.get(host).resize();
  }

  const resizeObserver = window.ResizeObserver ? new ResizeObserver(entries => entries.forEach(entry => draw(entry.target))) : null;
  function refresh() {
    panes.forEach(pane => {
      initialize(pane);
      if (visible(pane)) pane.querySelectorAll('.diagnostics-chart').forEach(draw);
    });
  }
  const observer = window.IntersectionObserver ? new IntersectionObserver(entries => entries.forEach(entry => {
    if (entry.isIntersecting) {
      initialize(entry.target);
      entry.target.querySelectorAll('.diagnostics-chart').forEach(draw);
    }
  })) : null;
  panes.forEach(pane => observer?.observe(pane));
  document.addEventListener('toggle', refresh, true);
  window.addEventListener('resize', refresh);
  if (!observer) window.addEventListener('scroll', refresh, {passive: true});
  refresh();
})();
