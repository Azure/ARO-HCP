(() => {
  'use strict';
  const data = JSON.parse(document.getElementById('db-sample-data').textContent);
  const decode = v => {
    if (v === null || typeof v !== 'object') return v;
    if (!Array.isArray(v) && Object.keys(v).length === 1 && Object.hasOwn(v, 's')) return data.Strings[v.s];
    if (Array.isArray(v)) return v.map(decode);
    return Object.fromEntries(Object.entries(v).map(([k, value]) => [k, decode(value)]));
  };
  const experiments = decode(data.Experiments) || [];
  const metric = document.getElementById('db-sample-metric'), windowSelect = document.getElementById('db-sample-window');
  const label = document.getElementById('db-sample-label'), selection = document.getElementById('db-sample-selection');
  const panel = document.getElementById('db-sample-label-panel'), values = document.getElementById('db-sample-values');
  const number = v => v == null ? 'Unavailable' : v.toLocaleString(undefined, {maximumFractionDigits: 3});
  const percent = v => v == null ? 'Unavailable' : `${number(v)}%`;
  const utc = v => new Date(v * 1000).toISOString();
  const element = (tag, text, parent) => { const e = document.createElement(tag); if (text != null) e.textContent = text; if (parent) parent.append(e); return e; };
  const option = (select, value, text) => { const o = element('option', text, select); o.value = value; };
  const table = (parent, headers) => {
    const wrap = element('div', null, parent); wrap.className = 'table-wrap';
    const t = element('table', null, wrap), tr = element('tr', null, element('thead', null, t));
    headers.forEach(h => element('th', h, tr)); return element('tbody', null, t);
  };
  const display = v => v.Remainder ? `Other values (exact remainder: ${v.Remainder})` : v.Value == null ? '(absent)' : v.Value === '' ? '(empty)' : v.Value;
  function valueTable(parent, rows) {
    const body = table(parent, ['Value', 'Series / groups', 'Stored samples', 'Samples/minute', 'Share of metric sampled window']);
    (rows || []).forEach(v => {
      const tr = element('tr', null, body);
      [display(v), number(v.Series), number(v.Samples), number(v.Rate)].forEach(s => element('td', s, tr));
      const td = element('td', percent(v.Share), tr);
      if (v.Share != null) { const bar = element('span', null, td); bar.className = 'db-sample-weight'; bar.style.width = `${Math.max(0, Math.min(100, v.Share))}%`; }
    });
  }
  const selected = () => experiments[Number(windowSelect.value)];
  function showLabel() {
    values.replaceChildren(); const e = selected(), l = (e.Labels || []).find(l => l.Name === label.value);
    if (!l) return;
    element('h3', `${l.Name}: ${number(l.Physical)} total physical series / ${number(l.Samples)} samples`, values);
    element('p', `${l.Distinct} distinct present values. All values, including absent, share the same metric/window sample denominator. Top 20 plus exact remainder.`, values);
    if (l.BaseIdentities != null) element('p', `${number(l.BaseIdentities)} base identities without ${l.Name}; physical/base ${number(l.CardinalityMultiplicity)}x; weighted sample multiplicity ${number(l.SampleMultiplicity)}x (total samples / sum of per-base maximum weights). Dropping this label does not reduce sample payloads.`, values);
    valueTable(values, l.Values);
  }
  function show() {
    const e = selected(); if (!e) return;
    selection.replaceChildren(); label.replaceChildren();
    element('h3', `${e.Workspace} / ${e.Metric} / ${e.Window}`, selection);
    const scope = element('p', 'Literal selector: ', selection); element('code', e.Selector, scope);
    element('p', `${utc(e.Start)} to ${utc(e.End)} | ${number(e.Minutes)} minutes | evaluation at ${utc(e.End)}`, selection);
    const status = element('p', e.Status, selection); status.className = e.Matched ? 'notice' : 'db-sample-warning';
    const stats = element('div', null, selection); stats.className = 'stats';
    [[e.Full.Series, 'All full-label physical series'], [e.Full.Samples, 'Full-label stored samples'], [e.Full.Rate, 'Full-label samples/minute']].forEach(([v, title]) => { const d = element('div', null, stats); element('strong', number(v), d); element('span', title, d); });
    element('p', `AMW mean ${number(e.AMWMean)} / observed maximum ${number(e.AMWPeak)} events/minute; paired valid minutes ${e.CoveredMinutes}/${e.ExpectedMinutes}. Approximate metric share of workspace AMW mean: ${percent(e.AMWShare)}.`, selection);
    const body = table(selection, ['Independent query', 'State', 'Returned groups / physical series', 'Stored samples', 'Samples/minute']);
    [['Grouped', e.Grouped], ['Full labels', e.Full]].forEach(([name, q]) => { const tr = element('tr', null, body); [name, q.State, number(q.Series), number(q.Samples), number(q.Rate)].forEach(s => element('td', s, tr)); });
    const labels = e.Labels || [];
    if (labels.length) {
      const body = table(selection, ['Label / distribution', 'Distinct values', 'Total physical series', 'Total samples', 'Base identities without replica label', 'Physical / base', 'Weighted sample multiplicity']);
      body.parentElement.className = 'db-sample-labels';
      labels.forEach(l => {
        option(label, l.Name, l.Name); const tr = element('tr', null, body), td = element('td', null, tr), button = element('button', l.Name, td);
        button.addEventListener('click', () => { label.value = l.Name; showLabel(); panel.scrollIntoView({block: 'nearest'}); });
        [number(l.Distinct), number(l.Physical), number(l.Samples), number(l.BaseIdentities), number(l.CardinalityMultiplicity), number(l.SampleMultiplicity)].forEach(s => element('td', s, tr));
      });
      label.value = labels.some(l => l.Name === 'prometheus_replica') ? 'prometheus_replica' : labels[0].Name;
    }
    panel.hidden = labels.length === 0; showLabel();
    if ((e.Sources || []).length) { const details = element('details', null, selection); element('summary', 'Grouped source measurements: cluster / job / namespace / HCP / Prometheus', details); element('p', 'Series column counts returned source groups here, not physical full-label series.', details); valueTable(details, e.Sources); }
    const provenance = element('details', null, selection); element('summary', 'Exact query provenance', provenance);
    [e.Grouped, e.Full].forEach(q => { element('h4', q.Kind, provenance); element('p', `ID ${q.ID} | accepted attempt ${number(q.Attempt)} | HTTP ${q.HTTP} | ${q.State}`, provenance); element('pre', q.Expression, provenance); element('pre', q.Params, provenance); });
  }
  const metrics = [...new Set(experiments.map(e => `${e.Workspace} / ${e.Selector}`))];
  metrics.forEach((m, i) => option(metric, i, m));
  function selectMetric() {
    windowSelect.replaceChildren(); experiments.forEach((e, i) => { if (`${e.Workspace} / ${e.Selector}` === metrics[Number(metric.value)]) option(windowSelect, i, `${e.Window} (${number(e.Minutes)} min)`); }); show();
  }
  metric.addEventListener('change', selectMetric); windowSelect.addEventListener('change', show); label.addEventListener('change', showLabel);
  selectMetric(); document.getElementById('db-sample-controls').hidden = false; document.getElementById('db-sample-fallback').open = false;
})();
