(() => {
  "use strict";
  const data = JSON.parse(document.getElementById("db-data").textContent);
  const el = id => document.getElementById(id);
  const metrics = (data.Metrics || []).map(m => ({...m, Name:data.Strings[m.Name]})), byID = new Map(metrics.map(m => [String(m.ID), m]));
  const rows = [...el("db-metrics").tBodies[0].rows];
  const format = v => v == null ? "Unavailable" : v.toLocaleString(undefined, {maximumFractionDigits: 1});
  const text = (tag, value) => { const n = document.createElement(tag); n.textContent = value; return n; };
  const label = v => v == null ? "(absent)" : v === "" ? "(empty)" : v;
  let chart, weight = "End", descending = true;
  const measured = m => m[weight] ?? (el("db-lower-bounds").checked ? m["Lower"+weight] : null);
  function select(id) {
    const m = byID.get(String(id));
    if (!m) return;
    el("db-selection").textContent = `${data.Workspaces[m.Workspace]} / ${m.Name}`;
    const body = el("db-source-table").tBodies[0]; body.replaceChildren();
    const tree = el("db-source-tree"); tree.replaceChildren();
    const root = document.createElement("details"); root.open = true;
    root.append(text("summary", data.Workspaces[m.Workspace])); tree.append(root);
    const clusters = new Map();
    for (const row of m.Sources || []) {
      // Matches the Go source tuple; absent labels use -1, empty labels are interned.
      const [Cluster, Job, Namespace, HCP] = row.slice(0,4).map(i => i < 0 ? null : data.Strings[i]);
      const [, , , , Groups, Remainder, Before, End, Delta, Samples, Rate] = row;
      const s = {Before, End, Delta, Samples, Rate};
      const cluster = Remainder ? `Remainder (${Groups} source groups)` : label(Cluster);
      const job = label(Job);
      const scope = Remainder ? "All omitted groups" : `${label(Namespace)} / ${label(HCP)}`;
      const tr = document.createElement("tr");
      for (const v of [cluster + (Remainder ? "" : " / " + job), scope, ...["Before", "End", "Delta", "Samples", "Rate"].map(k => format(s[k]))]) tr.append(text("td", v));
      body.append(tr);
      const clusterKey = Remainder ? row : Cluster;
      let c = clusters.get(clusterKey);
      if (!c) { const n = document.createElement("details"); n.append(text("summary", cluster)); root.append(n); c = {node:n,jobs:new Map()}; clusters.set(clusterKey,c); }
      let j = c.jobs.get(Job);
      if (!j) { j = document.createElement("details"); j.append(text("summary", job)); c.node.append(j); c.jobs.set(Job,j); }
      j.append(text("p", `${scope}: before ${format(s.Before)}, end ${format(s.End)}, net ${format(s.Delta)}, samples/min ${format(s.Rate)}`));
    }
    if (!(m.Sources || []).length) tree.append(text("p", "No source groups returned. Check parent-period coverage above."));
  }
  function render() {
    const filter = el("db-filter").value.toLowerCase();
    const visible = [];
    rows.sort((a,b) => {
      const av = measured(byID.get(a.dataset.id)), bv = measured(byID.get(b.dataset.id));
      if (av == null) return bv == null ? 0 : 1;
      if (bv == null) return -1;
      return descending ? bv-av : av-bv;
    });
    const fragment = document.createDocumentFragment();
    for (const row of rows) {
      const m = byID.get(row.dataset.id);
      row.hidden = !(m.Name + " " + data.Workspaces[m.Workspace]).toLowerCase().includes(filter);
      if (!row.hidden) visible.push(m);
      fragment.append(row);
    }
    el("db-metrics").tBodies[0].append(fragment);
    for (const button of document.querySelectorAll("[data-sort]")) button.parentElement.setAttribute("aria-sort", button.dataset.sort === weight ? (descending ? "descending" : "ascending") : "none");
    if (!chart) return;
    const workspaces = new Map();
    for (const m of visible) {
      const value = measured(m), partial = m[weight] == null;
      if (!(value > 0)) continue;
      if (!workspaces.has(m.Workspace)) workspaces.set(m.Workspace, {name:data.Workspaces[m.Workspace],children:[]});
      const workspace = workspaces.get(m.Workspace);
      workspace.partial = workspace.partial || partial;
      workspace.children.push({name:m.Name+(partial ? " (partial lower bound)" : ""),value,partial,metricID:m.ID});
    }
    chart.setOption({tooltip:{renderMode:"richText",formatter:p => `${p.name}: ${p.data?.partial ? ">=" : ""}${format(p.value)}`},series:[{type:"treemap",roam:false,nodeClick:"zoomToNode",breadcrumb:{show:true},upperLabel:{show:true},data:[...workspaces.values()]}]});
  }
  el("db-filter").addEventListener("input", render);
  el("db-lower-bounds").addEventListener("change", render);
  el("db-weight").addEventListener("change", () => { weight=el("db-weight").value; descending=true; render(); });
  for (const button of document.querySelectorAll("[data-sort]")) button.addEventListener("click", () => { descending=weight === button.dataset.sort ? !descending : true; weight=button.dataset.sort; el("db-weight").value=weight; render(); });
  for (const button of document.querySelectorAll(".db-select")) button.addEventListener("click", () => { select(button.dataset.metric); el("db-sources").scrollIntoView({block:"start"}); });
  function enhance() {
    if (!window.echarts || chart) return;
    try { el("db-treemap").hidden=false; chart=window.echarts.init(el("db-treemap")); chart.on("click", p => { if (p.data.metricID) select(p.data.metricID); }); render(); }
    catch { el("db-treemap").hidden=true; chart=null; }
  }
  el("db-echarts").addEventListener("load", enhance);
  window.addEventListener("resize", () => { if (chart) chart.resize(); });
  if (metrics.length) select(metrics[0].ID);
  render(); enhance();
})();
