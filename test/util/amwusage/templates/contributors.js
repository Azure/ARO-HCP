"use strict";
(() => {
  const model = JSON.parse(document.getElementById("contribution-data").textContent);
  const el = id => document.getElementById(id);
  const workspace = el("contribution-workspace"), grouping = el("contribution-group"), weight = el("contribution-weight");
  const suite = el("contribution-suite"), cohort = el("contribution-cohort");
  const routes = {source: ["cluster", "job", "metric", "namespace", "hostedcontrolplane"], customer: ["customer", "metric", "cluster", "job", "namespace", "hostedcontrolplane"], metric: ["metric", "cluster", "job", "namespace", "hostedcontrolplane"], collector: ["collector", "cluster", "job", "metric", "namespace", "hostedcontrolplane"], cohort: ["cohort", "customer", "metric", "cluster", "job", "namespace", "hostedcontrolplane"]};
  const titles = {cluster: "Source cluster", job: "Job", metric: "Metric", namespace: "Namespace", hostedcontrolplane: "HCP", collector: "Collector family", customer: "Customer cluster", cohort: "Period presence"};
  const measures = {runSeries: "Run series seen", newSeries: model.newLabel, samples: "Run stored samples", baselineSeries: "Baseline series seen", baselineSamples: "Baseline stored samples"};
  const number = n => n === null ? "Unknown" : n.toLocaleString(undefined, {maximumFractionDigits: 1});
  const signed = n => (n !== null && n > 0 ? "+" : "") + number(n);
  const percent = n => n === null ? "Unknown" : (n * 100).toFixed(2) + "%";
  const value = (row, dim) => (["metric", "collector", "customer", "cohort"].includes(dim) ? row[dim] : row.labels[dim]) || "";
  const key = text => text.toLowerCase();
  const dimensionKey = (row, dim) => dim === "customer" ? (row.customerID ? "id:" + row.customerID : "category:" + row.ownership) : key(value(row,dim));
  const display = text => text || "(absent)";
  const escape = text => String(text).replace(/[&<>"']/g, ch => ({"&":"&amp;", "<":"&lt;", ">":"&gt;", '"':"&quot;", "'":"&#39;"}[ch]));
  let path = [], chart = null;
  function aggregate(rows, measure) {
    const values = rows.map(r => r[measure]).filter(v => v !== null);
    const sum = values.reduce((a,b) => a+b, 0);
    return {total: values.length && Number.isFinite(sum) ? sum : null, known: values.length, count: rows.length, partial: rows.some(r => (r.partial || []).includes(measure))};
  }
  function annotated(a, sign = false) { return (sign ? signed(a.total) : number(a.total)) + (a.known < a.count || a.partial ? " (partial; " + a.known + "/" + a.count + " rows" + (a.partial ? "; response warning" : "") + ")" : ""); }
  function button(text, action) {
    const b = document.createElement("button"); b.type = "button"; b.textContent = text; b.addEventListener("click", action); return b;
  }
  function render() {
    const route = routes[grouping.value], measure = weight.value;
    const selected = model.rows.filter(r => (workspace.value === "" || r.workspace === Number(workspace.value)) && (suite.value === "" || r.ownership === suite.value) && (cohort.value === "" || r.cohort === cohort.value) && path.every((p,i) => dimensionKey(r,route[i]) === p.key));
    const total = aggregate(selected, measure);
    el("contribution-route").textContent = route.map(d => titles[d]).join(" / ");
    const crumb = el("contribution-breadcrumb"); crumb.replaceChildren();
    if (path.length) crumb.append(button("Back", () => { path.pop(); render(); }));
    crumb.append(button("All selected", () => { path = []; render(); }));
    path.forEach((p,i) => crumb.append(button(titles[route[i]] + ": " + display(p.name), () => { path = path.slice(0,i+1); render(); })));
    el("contribution-summary").textContent = annotated(total) + " " + measures[measure] + " in measured subtree. " + total.known + "/" + total.count + " rows measured.";
    const delta = aggregate(selected, "rateDelta");
    el("contribution-rates").textContent = "Run: " + annotated(aggregate(selected,"runRate")) + " samples/min; baseline: " + annotated(aggregate(selected,"baselineRate")) + " samples/min. Paired rate delta: " + annotated(delta,true) + " /min (" + delta.known + "/" + delta.count + " rows measured in both periods). Hypothesis, not causal shared load.";
    const owned = selected.filter(r => r.ownership === "Run-owned customer cluster");
    el("contribution-owned").textContent = model.contextValid ? "Run-owned within this subtree: " + annotated(aggregate(owned,"runSeries")) + " run series; " + annotated(aggregate(owned,"samples")) + " stored samples. Independent of baseline difference." : "Run-owned scope unavailable: no matching ownership context. Unassigned rows are not proof of background load.";
    const groups = new Map(), dimension = route[path.length];
    for (const row of selected) {
      const name = dimension ? value(row, dimension) : row.workspaceName + " / " + row.metric + " / prometheus: " + display(row.labels.prometheus);
      const id = dimension ? dimensionKey(row, dimension) : JSON.stringify([row.workspace, row.metric, row.labels]);
      if (!groups.has(id)) groups.set(id, {key: id, name, rows: []});
      groups.get(id).rows.push(row);
    }
    const nodes = [...groups.values()].map(g => ({...g, amount: aggregate(g.rows, measure)})).sort((a,b) => (b.amount.total ?? -1) - (a.amount.total ?? -1) || a.name.localeCompare(b.name));
    const tbody = el("contribution-table").querySelector("tbody"); tbody.replaceChildren();
    el("contribution-table-title").textContent = "Ranked " + (dimension ? titles[dimension] : "physical groups") + " contributions / " + measures[measure];
    let cumulative = 0;
    nodes.forEach((node,i) => {
      const tr = document.createElement("tr"), td = document.createElement("td");
      const drill = () => { path.push({key:node.key, name:node.name}); render(); };
      if (dimension) td.append(button((i+1) + ". " + display(node.name), drill)); else td.textContent = (i+1) + ". " + node.name;
      tr.append(td);
      for (const m of Object.keys(measures)) { const cell = document.createElement("td"); cell.textContent = annotated(aggregate(node.rows,m)); tr.append(cell); }
      const rates = document.createElement("td"); rates.textContent = annotated(aggregate(node.rows,"runRate")) + " / " + annotated(aggregate(node.rows,"baselineRate")); tr.append(rates);
      const rateDelta = document.createElement("td"); rateDelta.textContent = annotated(aggregate(node.rows,"rateDelta"),true); tr.append(rateDelta);
      const share = total.total > 0 && node.amount.total !== null ? node.amount.total / total.total : null;
      if (share !== null) cumulative += share;
      const shareCell = document.createElement("td"); shareCell.textContent = percent(share) + " / " + percent(share === null ? null : cumulative); tr.append(shareCell);
      const suggestions = [];
      if (node.rows.some(r => r.metric.toLowerCase().endsWith("_bucket"))) suggestions.push("Inspect histogram dimensions");
      if (node.rows.some(r => r.newSeries !== null && r.runSeries > 0 && r.newSeries/r.runSeries >= 0.5)) suggestions.push("Inspect target churn / lifecycle (not causality)");
      if ((measure === "samples" || measure === "baselineSamples") && share >= 0.2) suggestions.push("Review scrape interval/metric value");
      const hint = document.createElement("td"); hint.textContent = suggestions.join("; "); tr.append(hint); tbody.append(tr);
    });
    if (!nodes.length) { const tr = tbody.insertRow(); const td = tr.insertCell(); td.colSpan = 10; td.textContent = "No selected metric evidence."; }
    if (!chart) return;
    const positive = nodes.filter(n => n.amount.total > 0);
    el("treemap-status").textContent = positive.length ? "Click a tile or table row to drill down. Area and shares use measured values only; unknown and zero groups remain in the table." : "No positive measured weights to plot. Zero and unknown values remain distinct in the table.";
    chart.setOption({animation:false, tooltip:{confine:true, formatter:p => escape(p.data.name) + "<br>" + escape(annotated(p.data.amount)) + " " + escape(measures[measure]) + "<br>" + escape(percent(total.total > 0 ? p.data.value/total.total : null)) + " of measured subtree"}, series:[{type:"treemap", roam:false, nodeClick:false, breadcrumb:{show:false}, color:["#006f79", "#278e93", "#4b9f98", "#376a75", "#78aaa5"], label:{show:true, formatter:p => p.data.name + "\n" + number(p.data.value)}, itemStyle:{borderColor:"#f3f5f4",borderWidth:3,gapWidth:3}, data:positive.map(n => ({name:display(n.name),value:n.amount.total,amount:n.amount,key:n.key,displayName:n.name}))}]}, true);
    chart.off("click");
    chart.on("click", p => { if (dimension && p.data) { path.push({key:p.data.key, name:p.data.displayName}); render(); } });
  }
  function enableChart() {
    if (!window.echarts || chart || !el("sample-comparison").open) return;
    try { el("contribution-treemap").hidden = false; chart = window.echarts.init(el("contribution-treemap")); render(); }
    catch (_) { el("contribution-treemap").hidden = true; el("treemap-status").textContent = "Treemap unavailable. Use the ranked table and drill-down buttons."; }
  }
  weight.value = model.defaultWeight;
  el("treemap-status").textContent = "Loading optional ECharts treemap. If the CDN is unavailable, the ranked table and drill-down controls remain available.";
  el("contribution-controls").hidden = false;
  workspace.addEventListener("change", () => { path = []; render(); });
  grouping.addEventListener("change", () => { path = []; render(); });
  suite.addEventListener("change", () => { path = []; render(); });
  cohort.addEventListener("change", () => { path = []; render(); });
  weight.addEventListener("change", render);
  const library = el("echarts-library");
  library.addEventListener("load", enableChart);
  library.addEventListener("error", () => { el("treemap-status").textContent = "ECharts CDN unavailable. Ranked table and drill-down controls remain available."; });
  window.addEventListener("resize", () => { if (chart) chart.resize(); });
  el("sample-comparison").addEventListener("toggle", () => { enableChart(); if (chart) chart.resize(); });
  render(); enableChart();
  setTimeout(() => { if (!chart) el("treemap-status").textContent = "ECharts has not loaded (offline or blocked). Ranked table and drill-down controls remain available."; }, 5000);
})();
