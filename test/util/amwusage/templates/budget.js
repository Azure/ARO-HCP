(() => {
  'use strict';
  const $=id=>document.getElementById(id), data=JSON.parse($('budget-data').textContent);
  if (!data.Workspaces.length) return;
  const dimensions=['Metric','Namespace','Cluster','Scrape job','HCP label','Prometheus source'];
  const number=v=>v==null?'Unknown':v.toLocaleString('en-US',{maximumFractionDigits:2});
  const percent=v=>v==null?'Unknown':`${number(v)}%`;
  const share=(v,ref)=>v==null||!(ref>0)?null:v/ref*100;
  const make=(tag,text,parent)=>{const n=document.createElement(tag);if(text!=null)n.textContent=text;if(parent)parent.append(n);return n;};
  const button=(text,parent,action)=>{const n=make('button',text,parent);n.addEventListener('click',action);return n;};
   const svg=(tag,attrs,parent)=>{const n=document.createElementNS('http://www.w3.org/2000/svg',tag);for(const [k,v] of Object.entries(attrs))n.setAttribute(k,v);if(parent)parent.append(n);return n;};
   const legend=(parent,text,color,boundary=false)=>{const item=make('span',null,parent);item.className='legend-item';const square=make('span',null,item);square.className='legend-square'+(boundary?' legend-boundary':'');square.setAttribute('aria-hidden','true');if(color)square.style.backgroundColor=color;item.append(document.createTextNode(text));};
  const displayLabel=s=>s==null?'(absent)':s===''?'(empty)':s==='(absent)'||s==='(empty)'?JSON.stringify(s):s;
  const label=(dim,id)=>displayLabel(dim===0?data.Strings[data.Metrics[id].n]:id===-1?null:data.Strings[id]);
  data.Workspaces.sort((a,b)=>Number(b.Label==='Services')-Number(a.Label==='Services'));
  let workspace=data.Workspaces[0],mode='series',dimension=1,filters=[],page=0,sort='value',ranked=[],allRows=[],chart;
  const rate=()=>mode==='rate', reference=()=>rate()?workspace.Mean:workspace.ActiveEnd;
  const reconciliation=()=>rate()?workspace.Rate:workspace.Series;
  const metricFilter=()=>filters.find(f=>f[0]===0)?.[1];
  const sampleData=$('sample-data')?JSON.parse($('sample-data').textContent):[];
    const metricExperiments=(id=metricFilter())=>id==null?[]:sampleData.filter(e=>e.Workspace===workspace.Name&&e.Metric===label(0,id)&&(e.SourceReconciled??e.Matched));
    const labelAvailability=(id=metricFilter())=>{
      if(metricExperiments(id).length)return {available:true,text:'Label cardinality & sample distribution',title:'Inspect full-label measurements for selected windows; source filters do not change this metric/window scope'};
      const m=data.Metrics[id];
      if(m?.LabelSummaryIncluded)return {available:false,text:'Label comparison unavailable',title:'Label data is included, but it has not reconciled with every grouped source; no matched detail is available'};
      if(m?.LabelCollected)return {available:false,text:'Label detail not included',title:'Full label evidence is collected in SQLite. This bounded HTML report does not embed every metric summary'};
      return {available:false,text:'Label details not collected',title:'Grouped counts cannot reconstruct individual label cardinality'};
    };
  function timeline(){
    const c=rate()?workspace.RateChart:workspace.SeriesChart,host=$('timeline-svg'),width=Math.max(280,host.clientWidth);
    const x=v=>70+(Number(v)-62)*(width-85)/675,y=v=>10+(Number(v)-20)*80/162;
    host.replaceChildren();const s=svg('svg',{viewBox:`0 0 ${width} 115`,role:'img','aria-label':c.Title},host);
    const bands=rate()?workspace.RateBands:workspace.SeriesBands;
    for(const band of bands||[]){
      const left=70+band.Start*(width-85),right=70+band.End*(width-85),baseline=band.Name==='Previous baseline',color=baseline?'#7b8790':'#007c78';
      const area=svg('rect',{x:left,y:10,width:right-left,height:80,fill:color,'fill-opacity':0.025},s);svg('title',{},area).textContent=band.Name;
      let path='';for(let k=left-80;k<right;k+=9){const a=Math.max(left,k),b=Math.min(right,k+80),ya=baseline?10+a-k:90-a+k,yb=baseline?10+b-k:90-b+k;path+=`M${a},${ya}L${b},${yb}`;}
      svg('path',{d:path,fill:'none',stroke:color,'stroke-opacity':0.13,'stroke-width':0.7,'data-period':band.Name},s);
    }
    for(const t of c.Ticks||[]){svg('line',{x1:70,x2:width-15,y1:y(t.Position),y2:y(t.Position),stroke:'#dce3e0'},s);svg('text',{x:64,y:y(t.Position),dy:3,'text-anchor':'end',class:'axis'},s).textContent=t.Label;}
    for(const t of c.Times||[])svg('text',{x:x(t.Position),y:110,'text-anchor':t.Anchor,class:'axis'},s).textContent=t.Label;
    for(const t of c.RunMarkers||[])svg('line',{x1:x(t),x2:x(t),y1:10,y2:90,stroke:'#85928c','stroke-dasharray':'3 5'},s);
    const g=svg('g',{transform:`translate(70 10) scale(${(width-85)/675} ${80/162}) translate(-62 -20)`},s);
     for(const l of c.Lines||[])svg('path',{d:l.Path,fill:'none',stroke:l.Color,'stroke-width':1.5,'vector-effect':'non-scaling-stroke'},g);
     if(!c.HasData)svg('text',{x:width/2,y:55,'text-anchor':'middle',class:'axis'},s).textContent='No platform data';
     $('timeline-legend').replaceChildren();
     (c.Lines||[]).forEach((l,i)=>legend($('timeline-legend'),i===0?'Usage':'Limit',l.Color));
     for(const band of bands||[]){legend($('timeline-legend'),band.Name,null);$('timeline-legend').lastElementChild.firstElementChild.classList.add(band.Name==='Previous baseline'?'legend-baseline':'legend-run');}
  }
  function summary(){
    for(const b of $('workspaces').children)b.setAttribute('aria-pressed',String(Number(b.dataset.id)===workspace.ID));
    for(const b of $('modes').children)b.setAttribute('aria-pressed',String(b.dataset.mode===mode));
    $('cards').replaceChildren();
    const card=(title,value,description)=>{const a=make('article',null,$('cards'));make('h3',title,a);make('strong',number(value),a);make('p',description,a);};
    if(rate()){
      card('Peak load / events per minute',workspace.Peak,`${percent(workspace.PeakUtilization)} of ${number(workspace.PeakLimit)} same-minute limit | ${workspace.PeakTime}`);
      card('Average used / attribution reference',workspace.Mean,'Events/min; whole-run duration-weighted mean of AMW minute maxima.');
    }else{
      card('Series at run end',workspace.ActiveEnd,`${percent(workspace.ActiveUtilization)} of ${number(workspace.ActiveLimitEnd)} paired quota`);
      card('Net growth',workspace.ActiveDelta,`${number(workspace.ActiveBefore)} before -> ${number(workspace.ActiveEnd)} end; not run causality`);
    }
    timeline();const r=reconciliation();$('account-bar').replaceChildren();
    if(r.HasScale){const s=svg('svg',{viewBox:'0 0 600 24',preserveAspectRatio:'none',role:'img','aria-label':'Measured complete totals, measured lower bounds, and unaccounted AMW usage'},$('account-bar'));for(const [x,width,color] of [[0,r.CompleteWidth,'#007c78'],[r.CompleteWidth,r.PartialWidth,'#39958b'],[r.GapX,r.GapWidth,'#d49424']])svg('rect',{x,y:2,width,height:20,fill:color},s);svg('line',{x1:r.ReferenceX,x2:r.ReferenceX,y1:0,y2:24,stroke:'#173f43','stroke-width':2},s);}
     $('account-values').replaceChildren();
     const measured=r.Complete==null&&r.Partial==null?null:(r.Complete??0)+(r.Partial??0);
     legend($('account-values'),`Measured: complete ${number(r.Complete)} (${percent(r.CompleteShare)})`,'#007c78');
     legend($('account-values'),r.Partial==null?'Measured: at least (none)':`Measured: at least ${number(r.Partial)} (${percent(r.PartialShare)})`,'#39958b');
     legend($('account-values'),`Unaccounted ${number(r.Residual)} (${percent(r.ResidualShare)})`,'#d49424');
     $('account-values').className='chart-legend'+(r.Over!=null?' warning':'');
    $('confidence').textContent=`Measured ${number(measured)} (${percent(share(measured,reference()))}) in total. ${rate()?'Stored samples vs AMW received rate; shares use the run average, not peak.':'Sampled 12-hour series vs AMW end inventory.'} ${r.Over!=null?'Over-accounted; denominator unchanged. ':''}Gap includes measurement differences; not assigned to individual metrics.`;
  }
   // Aggregate complete parents and measured partial partitions, never a top-N.
   // Only complete parents can establish zero for an absent source/period.
  function aggregate(){
    const selectedMetric=metricFilter(),sourceFiltered=filters.some(f=>f[0]!==0);
     const metrics=selectedMetric==null?workspace.Metrics:[selectedMetric],flags=[0,0,0],complete=[0,0,0];
     for(const id of metrics){const m=data.Metrics[id],lower=[m.LowerBefore,m.LowerEnd,m.LowerSamples];[m.Before,m.End,m.Samples].forEach((v,i)=>{if(v!=null)complete[i]++;if(v!=null||lower[i]!=null)flags[i]++;});}
    const groups=new Map();
    if(dimension===0&&!sourceFiltered){
       for(const id of metrics){const m=data.Metrics[id],v=rate()?m.Samples:m.End,lower=rate()?m.LowerSamples:m.LowerEnd;groups.set(id,{id,b:m.Before??m.LowerBefore??null,e:m.End??m.LowerEnd??null,bExact:m.Before!=null,eExact:m.End!=null,bPartial:m.Before==null&&m.LowerBefore!=null,ePartial:m.End==null&&m.LowerEnd!=null,v:(v??lower)??null,partial:v==null&&lower!=null});}
    }else{
       for(const f of workspace.Facts||[]){let match=true;for(const [dim,id] of filters){if(f[dim]!==id){match=false;break;}}if(!match)continue;const key=f[dimension],v=data.Counts[f[6]];let g=groups.get(key);if(!g){g={id:key,b:0,e:0,s:0,mask:0};groups.set(key,g);}g.b+=v[0];g.e+=v[1];g.s+=v[2];g.mask|=f[7];}
      // A selected metric with a complete empty vector remains measured zero.
       if(dimension===0&&selectedMetric!=null&&!groups.size)groups.set(selectedMetric,{id:selectedMetric,b:0,e:0,s:0,mask:0});
       for(const g of groups.values()){
         const m=dimension===0?data.Metrics[g.id]:null,available=m?[m.Before,m.End,m.Samples].map((v,i)=>v!=null||!!(g.mask&(1<<i))):flags.map((known,i)=>known>0&&(complete[i]>0||!!(g.mask&(1<<i))));
         g.b=available[0]?g.b:null;g.e=available[1]?g.e:null;g.v=rate()?available[2]?g.s:null:g.e;g.partial=!!(g.mask&(rate()?4:2));g.bPartial=!!(g.mask&1);g.ePartial=!!(g.mask&2);
         g.bExact=!g.bPartial&&(m?m.Before!=null:complete[0]===metrics.length);g.eExact=!g.ePartial&&(m?m.End!=null:complete[1]===metrics.length);
      }
    }
    allRows=Array.from(groups.values());for(const g of allRows){
      // Subtracting two lower bounds cannot bound their difference.
      g.d=null;g.dBound='';
      if(g.b!=null&&g.e!=null){
        if(g.bExact&&g.eExact)g.d=g.e-g.b;
        else if(g.bExact&&g.ePartial){g.d=g.e-g.b;g.dBound='\u2265';}
        else if(g.bPartial&&g.eExact){g.d=g.e-g.b;g.dBound='\u2264';}
      }
      if(rate()&&g.v!=null)g.v/=data.Minutes;g.name=label(dimension,g.id);g.share=share(g.v,reference());
    }
    const q=$('search').value.toLowerCase();ranked=allRows.filter(g=>g.name.toLowerCase().includes(q));
    ranked.sort((a,b)=>((sort==='growth'&&!rate()?b.d:b.v)??-Infinity)-((sort==='growth'&&!rate()?a.d:a.v)??-Infinity)||a.name.localeCompare(b.name));
     $('scope').textContent=`${rate()?'Stored samples/min across the run.':'Series at end; growth is exact for complete endpoints, bounded when only one endpoint is a lower bound.'} Includes measured partial partitions; \u2265 denotes a lower bound, \u2264 an upper bound. Shares use ${workspace.Name} AMW, not the filtered subtotal.`;
  }
  function select(id){
    filters=filters.filter(f=>f[0]!==dimension);filters.push([dimension,id]);
    dimension=dimension===0?1:filters.some(f=>f[0]===0)?[2,3,5,4,1].find(d=>!filters.some(f=>f[0]===d))??dimension:0;
    $('dimension').value=dimension;page=0;$('search').value='';render();
  }
  function render(){
    const start=performance.now();aggregate();$('filters').replaceChildren();
    for(const [dim,id] of filters)button(`${dimensions[dim]}: ${label(dim,id)} [remove]`,$('filters'),()=>{filters=filters.filter(f=>f[0]!==dim);page=0;render();});
    if(filters.length){button('Back',$('filters'),()=>{filters.pop();page=0;render();});button('Reset',$('filters'),()=>{filters=[];page=0;$('search').value='';render();});}
    $('selection').textContent=metricFilter()==null?`${workspace.Name} / ${filters.length?'selected sources':'all sources'}`:`${workspace.Name} / ${label(0,metricFilter())}`;
     $('inspect').hidden=metricFilter()==null;
     const availability=labelAvailability();
     $('inspect').disabled=!availability.available;
     $('inspect').textContent=availability.text;
     $('inspect').title=availability.title;
    $('name-heading').textContent=dimensions[dimension];$('sort-value').textContent=rate()?'Samples/min':'Series at end';$('growth-heading').hidden=rate();
    page=Math.max(0,Math.min(page,Math.ceil(ranked.length/10)-1));$('rows').replaceChildren();
    for(const g of ranked.slice(page*10,page*10+10)){
       const tr=make('tr',null,$('rows')),name=make('td',null,tr);button(g.name,name,()=>select(g.id));
       if(dimension===0){const state=labelAvailability(g.id),details=button(state.available?'Labels':state.text,name,()=>{select(g.id);openLabels();});details.className='metric-labels';details.disabled=!state.available;details.title=state.title;details.setAttribute('aria-label',`Label details for ${g.name}: ${state.text}`);}
        const v=make('td',`${g.partial?'\u2265':''}${number(g.v)}`,tr);if(g.partial)make('small','Measured; remainder unknown',v);make('td',`${g.partial?'\u2265':''}${percent(g.share)}`,tr);if(!rate()){const cell=make('td',g.d==null?'Unknown':`${g.dBound|| (g.d>0?'+':'')}${number(g.d)}`,tr);if(g.bPartial&&g.ePartial)cell.title='Both endpoint totals are lower bounds; their difference does not bound net growth';make('small',`Before ${g.bPartial?'\u2265':''}${number(g.b)}`,cell);}
    }
    if(!ranked.length)make('td','No matching source values.',make('tr',null,$('rows'))).colSpan=rate()?3:4;
    $('page-status').textContent=`${ranked.length? page*10+1:0}-${Math.min(page*10+10,ranked.length)} of ${ranked.length}`;
    $('previous').disabled=page===0;$('next').disabled=(page+1)*10>=ranked.length;drawMap();
    $('explorer').dataset.renderMs=(performance.now()-start).toFixed(2);
  }
  function drawMap(){
    if($('treemap').hidden||typeof window.echarts?.init!=='function')return;
    if(!chart){chart=echarts.init($('treemap'));chart.on('click',p=>{if(p.data?.id!=null)select(p.data.id);});}
     const positive=allRows.filter(g=>g.v>0).sort((a,b)=>b.v-a.v),entries=positive.slice(0,50).map(g=>({id:g.id,name:g.name,value:g.v,partial:g.partial,itemStyle:{color:g.partial?'#39958b':'#007c78'}}));
     const omitted=positive.slice(50),rest=omitted.reduce((sum,g)=>sum+g.v,0),partial=omitted.some(g=>g.partial);if(rest)entries.push({name:`Other ${['metrics','namespaces','clusters','scrape jobs','HCP labels','Prometheus sources'][dimension]}`,value:rest,partial,itemStyle:{color:'#647b8a'}});
     if(!filters.length){const r=reconciliation();if(r.Residual>0)entries.push({name:'Unaccounted / AMW',value:r.Residual,itemStyle:{color:'#d49424'}});}
     chart.setOption({animation:false,tooltip:{show:false},series:[{type:'treemap',roam:false,nodeClick:false,breadcrumb:{show:false},left:0,right:0,top:0,bottom:0,label:{formatter:p=>`${p.name}\n${p.data.partial?'\u2265':''}${number(p.value)}`},itemStyle:{borderColor:'#fff',borderWidth:2},data:entries}]},true);chart.resize();
  }
  let experiments=[],labelPage=0,labelChoice=-1,choices=[];
  function labelWindow(){
    const e=experiments[Number($('label-window').value)];if(!e)return;
    $('label-title').textContent=e.Metric;
    $('label-scope').textContent=`${e.Selector} | ${new Date(e.Start*1000).toISOString()} to ${new Date(e.End*1000).toISOString()} | ${number(e.Full.Series)} physical series | ${number(e.Full.Rate)} samples/min`;
    if(e.SelectionReason)$('label-scope').textContent+=` | ${e.SelectionReason} (whole-run counts, across workspaces; not 12-hour AMW inventory)`;
    if(e.OmittedLabels)$('label-scope').textContent+=` | ${e.Labels.length} of ${e.LabelCount} labels shown; remaining detail in SQLite. Label reductions overlap and are not additive.`;
    choices=[...(e.Labels||[]).map(l=>({...l,title:l.Name})),...(e.LabelPairs||[]).map(l=>({...l,title:l.Names.join(' + ')}))];
    choices.sort((a,b)=>(b.Reduction??(b.BaseIdentities==null?-1:b.Physical-b.BaseIdentities))-(a.Reduction??(a.BaseIdentities==null?-1:a.Physical-a.BaseIdentities))||(b.Distinct??0)-(a.Distinct??0)||a.title.localeCompare(b.title));
    $('label-choice').replaceChildren();make('option','All labels: identity impact',$('label-choice')).value=-1;choices.forEach((l,i)=>{make('option',l.title,$('label-choice')).value=i;});labelChoice=-1;labelPage=0;showLabels();
  }
  function showLabels(){
    const e=experiments[Number($('label-window').value)],l=choices[labelChoice],rows=l?l.Values||[]:choices;
    $('label-choice').value=labelChoice;$('label-back').hidden=!l;$('label-head').replaceChildren();$('label-rows').replaceChildren();
     const h=make('tr',null,$('label-head'));(l?['Value','Physical series','Samples/min','Share of metric window']:['Label','Distinct values','Identities without label','Identity reduction','Expansion']).forEach(t=>make('th',t,h));
    $('label-summary').textContent=l?`${l.title}: ${number(l.Physical??e.Full.Series)} physical series; ${number(l.BaseIdentities)} identities without this label. Values use the selected metric/window denominator.`:'Labels ranked by measured identity reduction; not an estimate of achievable savings.';
    labelPage=Math.max(0,Math.min(labelPage,Math.ceil(rows.length/10)-1));
    for(const row of rows.slice(labelPage*10,labelPage*10+10)){
      const tr=make('tr',null,$('label-rows'));
      if(l){const v=row.Remainder?`Other (${row.Remainder} values)`:row.Values?row.Values.map((v,i)=>`${l.Names[i]}=${displayLabel(v)}`).join(' / '):displayLabel(row.Value);[v,number(row.Series),number(row.Rate),percent(row.Share)].forEach(t=>make('td',t,tr));}
       else{button(row.title,make('td',null,tr),()=>{labelChoice=choices.indexOf(row);labelPage=0;showLabels();});[number(row.Distinct),number(row.BaseIdentities),number(row.Reduction??(row.BaseIdentities==null?null:row.Physical-row.BaseIdentities)),row.CardinalityMultiplicity==null?'Unknown':`${number(row.CardinalityMultiplicity)}x`].forEach(t=>make('td',t,tr));}
    }
    $('label-page').textContent=`${rows.length?labelPage*10+1:0}-${Math.min(labelPage*10+10,rows.length)} of ${rows.length}`;$('label-previous').disabled=labelPage===0;$('label-next').disabled=(labelPage+1)*10>=rows.length;
  }
   function openLabels(){experiments=metricExperiments();if(!experiments.length)return;$('label-window').replaceChildren();experiments.forEach((e,i)=>{make('option',`${e.Window} (${number(e.Minutes)} min)`,$('label-window')).value=i;});labelWindow();$('labels').showModal();}
   $('inspect').addEventListener('click',openLabels);
  $('close-labels').addEventListener('click',()=>{$('labels').close();$('label-head').replaceChildren();$('label-rows').replaceChildren();});
  $('label-window').addEventListener('change',labelWindow);$('label-choice').addEventListener('change',()=>{labelChoice=Number($('label-choice').value);labelPage=0;showLabels();});$('label-back').addEventListener('click',()=>{labelChoice=-1;labelPage=0;showLabels();});$('label-previous').addEventListener('click',()=>{labelPage--;showLabels();});$('label-next').addEventListener('click',()=>{labelPage++;showLabels();});
  for(const w of data.Workspaces){const b=button(w.Label,$('workspaces'),()=>{workspace=w;filters=[];page=0;$('search').value='';summary();render();});b.dataset.id=w.ID;}
  for(const b of $('modes').children)b.addEventListener('click',()=>{mode=b.dataset.mode;sort='value';page=0;summary();render();});
  $('dimension').addEventListener('change',()=>{dimension=Number($('dimension').value);page=0;$('search').value='';render();});$('search').addEventListener('input',()=>{page=0;render();});
  $('previous').addEventListener('click',()=>{page--;render();});$('next').addEventListener('click',()=>{page++;render();});$('sort-value').addEventListener('click',()=>{sort='value';render();});$('sort-growth').addEventListener('click',()=>{sort='growth';render();});
    function setView(map){$('treemap').hidden=!map;$('treemap-legend').hidden=!map;$('table-view').hidden=map;$('map-toggle').setAttribute('aria-pressed',String(map));$('table-toggle').setAttribute('aria-pressed',String(!map));if(map)drawMap();}
   $('table-toggle').addEventListener('click',()=>setView(false));$('map-toggle').addEventListener('click',()=>setView(true));
   const enableMap=()=>{if(typeof window.echarts?.init==='function'){$('map-toggle').disabled=false;$('map-toggle').title='';}};$('chart-library').addEventListener('load',enableMap);enableMap();
  window.addEventListener('resize',()=>{timeline();chart?.resize();});$('app').hidden=false;$('fallback').remove();summary();render();
})();
