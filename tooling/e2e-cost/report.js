"use strict";
// initCostReport mounts the interactive parts of a cost report: inventories,
// resource details, and the treemap. It is shared by the single-file report and
// the site viewer, which supply the same data from embedded or fetched JSON.
function initCostReport(data) {
  const groups = data.inventory || [];
  const resources = groups.flatMap(group => (group.resources || []).map(resource => ({resource, group})));
  const byAnchor = new Map(resources.map(entry => [entry.resource.anchor, entry]));
  const known = value => value || "Unknown";
  const usd = value => "$" + value.toFixed(4);
  const node = (tag, text, parent, className) => {
    const element = document.createElement(tag);
    if (text !== undefined) element.textContent = text;
    if (className) element.className = className;
    if (parent) parent.append(element);
    return element;
  };
  const line = (parent, text, className) => {
    node("br", undefined, parent);
    return node("small", text, parent, className);
  };
  const net = (record, status) => !record.hasCharges ? "No billing records (not an explicit zero)" :
    usd(record.net) + (status !== "complete" ? " (Known subtotal)" : record.net === 0 ? " (Explicit net zero)" : "");
  const groupText = group => [group.name, group.subscriptionID, group.category, group.owner, group.kind,
    group.attribution, group.billingStatus, group.attempts, ...(group.outcomes || [])].join(" ").toLocaleLowerCase();
  // Index data once on first search, never scan rendered rows or rebuild text per keystroke.
  let groupIndex, resourceIndex;
  const searchIndex = () => {
    if (resourceIndex) return;
    groupIndex = groups.map(groupText);
    const groupSearch = new Map(groups.map((group, index) => [group, groupIndex[index]]));
    resourceIndex = resources.map(({resource: r, group}) => [groupSearch.get(group), r.id, r.name, r.type, r.source,
      net(r, group.billingStatus), r.postJob ? "Post-job usage" : "",
      ...(r.charges || []).map(c => [c.day, c.meterName, c.meterCategory, c.meterID, usd(c.costUSD)].join(" "))
    ].join(" ").toLocaleLowerCase());
  };
  // One bounded table implementation for inventory, audit, and selected-resource charges.
  const table = (host, records, size, noun, headers, renderRow, search) => {
    let matches = records, page = 0;
    if (search) {
      const label = node("label", search.label, host);
      label.htmlFor = search.id;
      const input = node("input", undefined, host);
      input.id = search.id;
      input.type = "search";
      input.autocomplete = "off";
      input.placeholder = "Filter this offline inventory";
      let timer;
      input.addEventListener("input", () => {
        clearTimeout(timer);
        timer = setTimeout(() => {
          const query = input.value.trim().toLocaleLowerCase();
          const index = search.index();
          matches = query ? records.filter((_, i) => index[i].includes(query)) : records;
          page = 0;
          draw();
        }, 150);
      });
    }
    const pager = node("div", undefined, host, "pager");
    const previous = node("button", "Prev", pager);
    const count = node("p", undefined, pager, "muted");
    count.setAttribute("aria-live", "polite");
    if (search) count.id = search.id === "search" ? "matches" : search.id.replace(/-search$/, "-matches");
    const next = node("button", "Next", pager);
    const wrap = node("div", undefined, host, "table-wrap");
    const grid = node("table", undefined, wrap);
    const heading = node("tr", undefined, node("thead", undefined, grid));
    headers.forEach(text => node("th", text, heading));
    const body = node("tbody", undefined, grid);
    const draw = () => {
      body.replaceChildren();
      const start = page * size;
      matches.slice(start, start + size).forEach(record => renderRow(node("tr", undefined, body), record));
      if (!matches.length) {
        node("td", "No matching " + noun + ".", node("tr", undefined, body)).colSpan = headers.length;
      }
      count.textContent = matches.length + " of " + records.length + " " + noun +
        (matches.length ? " | " + (start + 1) + "-" + Math.min(start + size, matches.length) : "");
      previous.disabled = page === 0;
      next.disabled = start + size >= matches.length;
    };
    previous.addEventListener("click", () => { page--; draw(); });
    next.addEventListener("click", () => { page++; draw(); });
    draw();
  };
  const selected = document.getElementById("selected-resource");
  const openResource = id => {
    const entry = byAnchor.get(id);
    if (!entry) return;
    const {resource: r, group: g} = entry;
    selected.replaceChildren();
    selected.hidden = false;
    node("h2", "Resource details", selected);
    const detail = node("details", undefined, selected, "resource" + (r.postJob ? " post-job" : ""));
    detail.id = id;
    detail.open = true;
    const title = node("summary", r.name, detail);
    if (r.postJob) node("span", "Post-job usage", title, "badge");
    const fields = node("dl", undefined, detail);
    const field = (label, value) => { node("dt", label, fields); node("dd", value, fields); };
    field("Full resource ID", r.id || "Unavailable (unattributed charge)");
    field("Resource group", g.name);
    field("Subscription", known(g.subscriptionID));
    field("Owner", g.owner || "Unknown owner");
    field("Group kind", known(g.kind));
    if (g.attribution) field("Attribution", g.attribution);
    field("Type / source", r.type + " / " + known(r.source));
    field("Billing status", known(g.billingStatus));
    if (!data.subscriptionMode) {
      field("Attempts", g.attempts || "Unavailable");
      field("Outcomes", (g.outcomes || []).join(", ") || "Unavailable");
    }
    field("Net cost", net(r, g.billingStatus));
    if (r.charges && r.charges.length) {
      const charges = node("div", undefined, detail);
      charges.id = "charges";
      table(charges, r.charges, 50, "meter rows", ["Usage day (UTC)", "Meter", "Category / meter ID", "USD"], (row, c) => {
        node("td", c.day, row);
        node("td", known(c.meterName), row);
        line(node("td", known(c.meterCategory), row), known(c.meterID));
        node("td", usd(c.costUSD), row, "money");
      });
    }
    if (location.hash !== "#" + id) location.hash = id;
    detail.scrollIntoView({block: "start"});
  };
  const fromHash = () => {
    const id = location.hash.slice(1);
    if (!selected.querySelector("details") || selected.querySelector("details").id !== id) openResource(id);
  };
  window.addEventListener("hashchange", fromHash);
  fromHash();
  const lazy = (id, mount) => {
    const shell = document.getElementById(id);
    let mounted = false;
    shell.addEventListener("toggle", () => {
      if (shell.open && !mounted) { mounted = true; mount(); }
    });
  };
  lazy("group-inventory", () => table(document.getElementById("group-browser"), groups, 25, "groups",
    ["Resource group / subscription", "Owner", "Billing status", "Resources", "Net USD"], (row, g) => {
      const name = node("td", g.name, row);
      line(name, known(g.subscriptionID));
      if (g.attribution) line(name, "Attribution: " + g.attribution);
      line(node("td", g.category + " / " + known(g.owner), row), g.kind);
      const status = node("td", known(g.billingStatus), row);
      if (g.billingStatus === "complete" && !g.hasCharges) line(status, "No billing records");
      const count = (g.resources || []).length;
      node("td", count ? count : "0 (empty)", row);
      node("td", net(g, g.billingStatus), row, "money");
    }, {id: "group-search", label: "Search groups, subscriptions, owners, or attribution", index: () => { searchIndex(); return groupIndex; }}));
  lazy("resource-inventory", () => table(document.getElementById("resource-browser"), resources, 50, "resources",
    ["Resource / type", "Resource group / owner", "Source / billing", "Net USD"], (row, {resource: r, group: g}) => {
      row.dataset.resource = r.anchor;
      if (r.postJob) row.className = "post-job";
      const name = node("td", undefined, row);
      const link = node("a", r.name, name);
      link.href = "#" + r.anchor;
      link.addEventListener("click", event => { event.preventDefault(); openResource(r.anchor); });
      line(name, r.type);
      if (r.postJob) line(name, "Post-job usage", "badge");
      line(node("td", g.name, row), g.owner || "Unknown owner");
      node("td", known(r.source) + " / " + known(g.billingStatus), row);
      node("td", net(r, g.billingStatus), row, "money");
    }, {id: "search", label: "Search resources, IDs, groups, owners, attribution, or meters", index: () => { searchIndex(); return resourceIndex; }}));
  if (data.subscriptionMode) {
    const subscriptionNames = new Map((data.subscriptions || [])
      .map(sub => [sub.id.toLowerCase(), sub.displayName || ""]));
    const subscriptionName = id => subscriptionNames.get(id.toLowerCase()) || "";
    lazy("filtered-inventory", () => {
      const filtered = data.filtered || [];
      let index;
      table(document.getElementById("filtered-browser"), filtered, 25, "filtered groups",
        ["Subscription", "Resource group", "Filter reason", "Excluded USD"], (row, g) => {
          const cell = node("td", undefined, row);
          const name = subscriptionName(g.subscriptionID);
          if (name) {
            node("strong", name, cell);
            node("br", undefined, cell);
          }
          node("span", g.subscriptionID, cell);
          node("td", g.name, row);
          node("td", g.reason, row);
          node("td", usd(g.costUSD), row, "money");
        }, {id: "filtered-search", label: "Search filtered subscriptions, groups, reasons, or costs", index: () => {
          if (!index) index = filtered.map(g => [g.subscriptionID, subscriptionName(g.subscriptionID), g.name, g.reason, usd(g.costUSD)].join(" ").toLocaleLowerCase());
          return index;
        }});
    });
  }
  const element = document.getElementById("treemap");
  if (element) {
    const chart = echarts.init(element, null, {renderer: "canvas"});
    const option = data.option;
    const ownershipData = option.series[0].data;
    const typeData = data.typeData || [];
    chart.setOption(option);
    document.getElementById("chart-grouping").addEventListener("change", event => {
      const byType = event.target.value === "type";
      option.series[0].data = byType ? typeData : ownershipData;
      option.series[0].leafDepth = byType ? 1 : 2;
      chart.setOption(option, {notMerge: true});
    });
    chart.on("click", params => {
      if (params.data && params.data.resource) openResource(params.data.resource);
    });
    window.addEventListener("resize", () => chart.resize());
  }
}
