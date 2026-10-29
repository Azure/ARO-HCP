"use strict";
(() => {
  const model = JSON.parse(document.getElementById("active-data").textContent);
  const el = id => document.getElementById(id), number = n => n.toLocaleString(), signed = n => (n > 0 ? "+" : "") + number(n);
  const rows = model.rows, workspace = el("active-workspace"), group = el("active-group");
  const routes = {source:["workspace","collector","cluster","job","scope","metric"], metric:["metric","workspace","collector","cluster","job","scope"], collector:["workspace","collector","cluster","agent","job","scope","metric"]};
  const title = {workspace:"Workspace",collector:"Collector family",scope:"Scope",metric:"Metric",agent:"Agent",cluster:"Source cluster",job:"Scrape job"};
  let period = "before", path = [], chart = null, selectedLabel = "";
  const dim = (r,d) => d === "workspace" ? model.workspaceNames[r.workspace] : d === "agent" ? (r.labels.prometheus_replica || "(absent)") : d === "cluster" || d === "job" ? (r.labels[d] || "(absent)") : r[d];
  const dimKey = (r,d) => d === "agent" ? JSON.stringify([r.workspace,r.labels.cluster??null,r.labels.prometheus_replica??null]) : d === "scope" ? r.scopeKey : d === "workspace" ? String(r.workspace) : dim(r,d);
  const chosen = r => period === "before" ? r.before === true : period === "end" ? r.end === true : r.end === true && r.before === false;
  const removed = r => period === "growth" && r.before === true && r.end === false;
  const escape = s => String(s).replace(/[&<>"']/g, c => ({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"}[c]));
  function button(text,fn) { const b=document.createElement("button");b.type="button";b.textContent=text;b.addEventListener("click",fn);return b; }
  function cell(tr,text) { const td=document.createElement("td");td.textContent=text;tr.append(td);return td; }
  function identity(r,drop) { return JSON.stringify([r.workspace,r.metric,Object.keys(r.labels).sort().filter(k=>k!==drop).map(k=>[k,r.labels[k]])]); }
  function details(selected,metricSelected) {
    el("active-label-detail").hidden = !metricSelected;
    if (!metricSelected) return;
    const labels=[...new Set(selected.flatMap(r=>Object.keys(r.labels)))];
    const stats=labels.map(label=>{
      const counts=new Map();let missing=0;const projected=new Set();
      for(const r of selected){projected.add(identity(r,label));if(Object.hasOwn(r.labels,label)){const v=r.labels[label];counts.set(v,(counts.get(v)||0)+1);}else missing++;}
      return {label,counts,missing,projected:projected.size,reduction:selected.length-projected.size};
    }).sort((a,b)=>b.reduction-a.reduction||a.label.localeCompare(b.label));
    const body=el("active-labels").querySelector("tbody");body.replaceChildren();
    for(const s of stats){const tr=document.createElement("tr");const td=cell(tr,"");td.append(button(s.label,()=>{selectedLabel=s.label;values(s);}));cell(tr,number(s.counts.size));cell(tr,number(s.missing));cell(tr,number(s.projected));cell(tr,number(s.reduction));body.append(tr);}
    function values(s){
      const body=el("active-label-values").querySelector("tbody");body.replaceChildren();
      el("active-label-title").textContent=s ? "Label values / "+s.label : "No label values in this selection";
      if(!s)return;
      const counts=[...s.counts].map(([name,count])=>({name,count}));if(s.missing)counts.push({name:"(missing)",count:s.missing});counts.sort((a,b)=>b.count-a.count||a.name.localeCompare(b.name));
      for(const v of counts){const tr=document.createElement("tr");cell(tr,v.name);cell(tr,number(v.count));const td=cell(tr,(100*v.count/selected.length).toFixed(2)+"%");const bar=document.createElement("progress");bar.max=selected.length;bar.value=v.count;td.append(bar);body.append(tr);}
    }
    values(stats.find(s=>s.label===selectedLabel)||stats[0]);
    const projected=new Map(), agents=new Map();
    for(const r of selected){const id=identity(r,"prometheus_replica");projected.set(id,(projected.get(id)||0)+1);const key=JSON.stringify([r.workspace,r.labels.cluster||"",r.labels.prometheus_replica||""]);if(!agents.has(key))agents.set(key,{cluster:(model.workspaceNames[r.workspace]+" / "+(r.labels.cluster||"(absent)")),name:r.labels.prometheus_replica||"",count:0,ids:new Set()});const a=agents.get(key);a.count++;a.ids.add(id);}
    const hist=new Map();for(const copies of projected.values())hist.set(copies,(hist.get(copies)||0)+1);
    el("active-replicas").textContent=number(selected.length)+" physical series; "+number(projected.size)+" identities without only prometheus_replica; observed mean multiplicity "+(projected.size ? (selected.length/projected.size).toFixed(2) : "unknown")+".";
    el("active-multiplicity").textContent=[...hist].sort((a,b)=>a[0]-b[0]).map(([copies,n])=>number(n)+" identities with "+copies+" physical copies").join("; ");
    const ab=el("active-agents").querySelector("tbody");ab.replaceChildren();
    for(const a of [...agents.values()].sort((a,b)=>a.cluster.localeCompare(b.cluster)||a.name.localeCompare(b.name))){const match=a.name.match(/^prom-agent-prometheus(?:-shard-([0-9]+))?-([01])$/);const tr=document.createElement("tr");cell(tr,a.cluster);cell(tr,a.name||"(absent)");cell(tr,match ? match[1]||"0" : "Unknown");cell(tr,number(a.count));cell(tr,number(a.ids.size));ab.append(tr);}
    const shards=new Map(), identityShards=new Map(), targetShards=new Map();let unknown=0;
    for(const r of selected){
      const match=(r.labels.prometheus_replica||"").match(/^prom-agent-prometheus(?:-shard-([0-9]+))?-([01])$/);
      const shard=match?match[1]||"0":"Unknown", cluster=model.workspaceNames[r.workspace]+" / "+(r.labels.cluster||"(absent)");
      const k=JSON.stringify([r.workspace,r.labels.cluster||"",shard]);
      if(!shards.has(k))shards.set(k,{cluster,shard,count:0,ids:new Set(),targets:new Set()});
      const s=shards.get(k), id=identity(r,"prometheus_replica"), target=JSON.stringify([r.workspace,...["cluster","job","namespace","instance"].map(l=>r.labels[l]??null)]);
      s.count++;s.ids.add(id);s.targets.add(target);
      if(!match){unknown++;continue;}
      if(!identityShards.has(id))identityShards.set(id,new Set());identityShards.get(id).add(shard);
      if(!targetShards.has(target))targetShards.set(target,new Set());targetShards.get(target).add(shard);
    }
    el("active-shard-overlap").textContent=[...identityShards.values()].filter(s=>s.size>1).length+" replica-free identities and "+[...targetShards.values()].filter(s=>s.size>1).length+" target tuples appear across recognized shards. "+unknown+" series have unknown agent naming. Target tuples retain workspace, cluster, job, namespace and instance; missing fields remain missing. This is observed selected membership, not deployment configuration.";
    const sb=el("active-shards").querySelector("tbody");sb.replaceChildren();
    for(const s of [...shards.values()].sort((a,b)=>a.cluster.localeCompare(b.cluster)||a.shard.localeCompare(b.shard))){const tr=document.createElement("tr");cell(tr,s.cluster);cell(tr,s.shard);cell(tr,number(s.count));cell(tr,number(s.ids.size));cell(tr,number(s.targets.size));sb.append(tr);}
  }
  function render(){
    const route=routes[group.value];
    const selected=rows.filter(r=>(workspace.value===""||r.workspace===Number(workspace.value))&&path.every((p,i)=>dimKey(r,route[i])===p.key));
    const positive=selected.filter(chosen), negative=selected.filter(removed);
    const coverage=model.coverage.filter(c=>(workspace.value===""||c.workspace===Number(workspace.value))&&path.every((p,i)=>route[i]==="workspace"?String(c.workspace)===p.key:route[i]==="metric"?c.metric===p.key:true));
    const known=coverage.filter(c=>period==="before"?c.Before:period==="end"?c.End:c.Before&&c.End).length;
    const coverageNote=" "+known+"/"+coverage.length+" metric inventories measured for this period (workspace/metric scope).";
    el("active-period-note").textContent=period==="before" ? "Baseline inventory: full physical label sets retained in the 12 hours before run start. Not the separate quiet-window sample baseline." : period==="end" ? "Capacity inventory: full physical label sets retained in the 12 hours before run end." : "Growth: added full identities supply treemap area; removals and signed net are separate. Added is not first-ever creation or proof of run causality.";
    el("active-selection").textContent=(known===0 ? "Unavailable for this period; missing snapshots are not absence." : period==="growth" ? number(positive.length)+" added; "+number(negative.length)+" removed; "+signed(positive.length-negative.length)+" net in measured comparisons only." : number(positive.length)+" physical series in measured snapshots only.")+coverageNote;
    el("active-count-heading").textContent=period==="growth" ? "Added identities" : "Selected series";
    const crumb=el("active-breadcrumb");crumb.replaceChildren();if(path.length)crumb.append(button("Back",()=>{path.pop();render();}));crumb.append(button("All inspected",()=>{path=[];render();}));path.forEach((p,i)=>crumb.append(button(title[route[i]]+": "+p.name,()=>{path=path.slice(0,i+1);render();})));
    const d=route[path.length], groups=new Map();
    for(const r of selected){if(!chosen(r)&&!removed(r))continue;const key=d?dimKey(r,d):"all",name=d?dim(r,d):"Selected full identities";if(!groups.has(key))groups.set(key,{key,name,count:0,removed:0});const node=groups.get(key);if(chosen(r))node.count++;if(removed(r))node.removed++;}
    const nodes=[...groups.values()].sort((a,b)=>b.count-a.count||a.name.localeCompare(b.name));const body=el("active-ranked").querySelector("tbody");body.replaceChildren();let cumulative=0;
    for(const n of nodes){const tr=document.createElement("tr"),td=cell(tr,"");if(d)td.append(button(n.name,()=>{path.push({key:n.key,name:n.name});render();}));else td.textContent=n.name;cell(tr,number(n.count));cell(tr,period==="growth"?number(n.removed):"Not compared");cell(tr,period==="growth"?signed(n.count-n.removed):"Not compared");cumulative+=n.count;cell(tr,positive.length?(100*n.count/positive.length).toFixed(2)+"% / "+(100*cumulative/positive.length).toFixed(2)+"%":"No positive weight");body.append(tr);}
    if(!nodes.length){const tr=document.createElement("tr");const td=cell(tr,"No measured identities in this period/selection. See inventory coverage above.");td.colSpan=5;body.append(tr);}
    details(positive,path.some((_,i)=>route[i]==="metric"));
    if(!chart)return;
    el("active-status").textContent="Area and 100% shares refer only to inspected series, never the workspace quota. Click a tile or table row to drill down.";
    chart.setOption({animation:false,tooltip:{confine:true,formatter:p=>escape(p.data.name)+"<br>"+number(p.data.value)+" inspected physical series"},series:[{type:"treemap",roam:false,nodeClick:false,breadcrumb:{show:false},color:["#006f79","#278e93","#4b9f98","#376a75","#78aaa5"],label:{formatter:p=>p.name+"\n"+number(p.value)},itemStyle:{borderColor:"#f3f5f4",borderWidth:3,gapWidth:3},data:nodes.filter(n=>n.count>0).map(n=>({name:n.name,value:n.count,key:n.key}))}]},true);
    chart.off("click");chart.on("click",p=>{if(d&&p.data){path.push({key:p.data.key,name:p.data.name});render();}});
  }
  function enable(){if(!window.echarts||chart)return;try{el("active-treemap").hidden=false;chart=echarts.init(el("active-treemap"));render();}catch(_){el("active-treemap").hidden=true;el("active-status").textContent="Treemap unavailable; ranked table and label details remain available.";}}
  el("active-controls").hidden=false;
  document.querySelectorAll("[data-period]").forEach(b=>b.addEventListener("click",()=>{period=b.dataset.period;document.querySelectorAll("[data-period]").forEach(p=>p.setAttribute("aria-pressed",String(p===b)));render();}));
  group.addEventListener("change",()=>{path=[];render();});workspace.addEventListener("change",()=>{path=[];render();});
  el("echarts-library").addEventListener("load",enable);el("echarts-library").addEventListener("error",()=>{el("active-status").textContent="ECharts CDN unavailable. Use the ranked table and label details.";});
  window.addEventListener("resize",()=>{if(chart)chart.resize();});render();enable();
})();
