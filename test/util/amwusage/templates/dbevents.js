;
(() => {
  'use strict';
  const data = JSON.parse(document.getElementById('db-events-data').textContent);
  const rows = [...document.querySelectorAll('[data-namespace-workspace]')];
  const label = value => value === null ? '(absent)' : value === '' ? '(empty)' : value;
  const number = value => value === null ? 'Unavailable' : new Intl.NumberFormat('en-US', {maximumFractionDigits: 2}).format(value);
  function select(row) {
    const scope = data[row.dataset.namespaceWorkspace][Number(row.dataset.namespaceIndex)];
    if (scope.Remainder) return;
    for (const r of rows) r.setAttribute('aria-selected', String(r === row));
    const workspace = row.closest('article').querySelector('h3').textContent;
    document.getElementById('db-namespace-selection').textContent = workspace + ' / ' + label(scope.Cluster) + ' / ' + label(scope.Namespace) + ' / HCP: ' + label(scope.HCP);
    const body = document.getElementById('db-namespace-metrics');
    body.replaceChildren();
    for (const metric of scope.Metrics || []) {
      const tr = document.createElement('tr');
      const name = document.createElement('td');
      name.textContent = metric.Name + (metric.Remainder ? ' (' + metric.Count + '; accepted before/end/run: ' + metric.BeforeMetrics + '/' + metric.EndMetrics + '/' + metric.RunMetrics + ')' : '');
      tr.append(name);
      for (const key of ['Before', 'End', 'Delta', 'Samples', 'Rate']) {
        const td = document.createElement('td'); td.textContent = number(metric[key]); tr.append(td);
      }
      body.append(tr);
    }
  }
  for (const row of rows) {
    const button = row.querySelector('button');
    if (!button) continue;
    button.addEventListener('click', () => select(row));
    button.addEventListener('focus', () => select(row));
    row.addEventListener('mouseenter', () => select(row));
  }
  document.getElementById('db-namespace-filter').addEventListener('input', event => {
    const filter = event.target.value.toLowerCase().trim();
    for (const row of rows) {
      const scope = data[row.dataset.namespaceWorkspace][Number(row.dataset.namespaceIndex)];
      const workspace = row.closest('article').querySelector('h3').textContent;
      row.hidden = !scope.Remainder && !(workspace + ' ' + label(scope.Cluster) + ' ' + label(scope.Namespace) + ' ' + label(scope.HCP)).toLowerCase().includes(filter);
    }
  });
  if (rows.length) select(rows[0]);
})();
