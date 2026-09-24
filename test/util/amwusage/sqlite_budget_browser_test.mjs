// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// AMW_USAGE_SCREENSHOTS=/path/to/screenshots node sqlite_budget_browser_test.mjs usage.html
import {spawn} from 'node:child_process';
import assert from 'node:assert/strict';
import {writeFile} from 'node:fs/promises';
import {pathToFileURL} from 'node:url';

const chrome=spawn(process.env.CHROME||'/usr/bin/google-chrome',['--headless','--no-sandbox','--disable-gpu','--disable-background-networking','--no-first-run','--no-default-browser-check','--remote-debugging-port=0','about:blank']);
let socket;
try {
  const endpoint=await new Promise((resolve,reject)=>{let stderr='';const timer=setTimeout(()=>reject(Error('Chrome startup timeout')),20000);chrome.stderr.on('data',chunk=>{stderr+=chunk;const match=stderr.match(/DevTools listening on (ws:\/\/[^\s]+)/);if(match){clearTimeout(timer);resolve(match[1]);}});chrome.on('error',reject);});
  socket=new WebSocket(endpoint);await new Promise(resolve=>socket.addEventListener('open',resolve,{once:true}));
  let id=0;const pending=new Map(),errors=[];
  socket.addEventListener('message',event=>{const msg=JSON.parse(event.data);if(msg.method==='Runtime.exceptionThrown')errors.push(msg.params);if(msg.id){const p=pending.get(msg.id);pending.delete(msg.id);msg.error?p.reject(Error(JSON.stringify(msg.error))):p.resolve(msg.result);}});
  const call=(method,params={},sessionId)=>new Promise((resolve,reject)=>{pending.set(++id,{resolve,reject});socket.send(JSON.stringify({id,method,params,sessionId}));});
  const {targetId}=await call('Target.createTarget',{url:'about:blank'}),{sessionId}=await call('Target.attachToTarget',{targetId,flatten:true});
  const page=(method,params)=>call(method,params,sessionId);
  const evaluate=async expression=>{const r=await page('Runtime.evaluate',{expression,returnByValue:true,awaitPromise:true});if(r.exceptionDetails)throw Error(JSON.stringify(r.exceptionDetails));return r.result.value;};
  const wait=async expression=>{for(let i=0;i<300;i++){if(await evaluate(expression))return;await new Promise(r=>setTimeout(r,50));}throw Error('Timeout: '+expression);};
  await page('Runtime.enable');await page('Network.enable');await page('Page.enable');await page('Network.setCacheDisabled',{cacheDisabled:true});
  let maxDOM=0,initialDOM=0,maxMs=0;
  const measure=async()=>{const v=await evaluate(`({dom:document.querySelectorAll('*').length,ms:Number(document.getElementById('explorer').dataset.renderMs)})`);maxDOM=Math.max(maxDOM,v.dom);maxMs=Math.max(maxMs,v.ms);assert.ok(v.dom<800,`DOM ${v.dom}`);};
  const dimension=async d=>{await evaluate(`document.getElementById('dimension').value=${JSON.stringify(String(d))};document.getElementById('dimension').dispatchEvent(new Event('change'))`);await measure();};
  const reset=async()=>{await evaluate(`Array.from(document.querySelectorAll('#filters button')).find(b=>b.textContent==='Reset')?.click()`);};
  const choose=async name=>{await evaluate(`(()=>{document.getElementById('search').value=${JSON.stringify(name)};document.getElementById('search').dispatchEvent(new Event('input'));const b=Array.from(document.querySelectorAll('#rows button')).find(b=>b.textContent===${JSON.stringify(name)});if(!b)throw Error('Missing row '+${JSON.stringify(name)});b.click()})()`);await measure();};
  for(const blocked of [true,false]){
    await page('Network.setBlockedURLs',{urls:blocked?['*cdn.jsdelivr.net*']:[]});
    for(const nojs of blocked?[false,true]:[false]){
      await page('Emulation.setScriptExecutionDisabled',{value:nojs});
      for(const width of [1440,390,320]){
        await page('Emulation.setDeviceMetricsOverride',{width,height:1000,deviceScaleFactor:1,mobile:width<700});await page('Page.navigate',{url:pathToFileURL(process.argv[2]).href});
        await wait(`document.readyState==='complete'&&!!document.getElementById('explorer')`);
        assert.equal(await evaluate('document.documentElement.scrollWidth>innerWidth'),false,`overflow ${width} nojs=${nojs}`);
        assert.equal(await evaluate(`!!document.getElementById('advanced')||!!document.getElementById('leads')`),false);
        if(nojs){assert.ok(await evaluate(`document.querySelectorAll('#fallback tbody tr').length<=2`));continue;}
        initialDOM=Math.max(initialDOM,await evaluate(`document.querySelectorAll('*').length`));assert.ok(initialDOM<250,`initial DOM ${initialDOM}`);await measure();
        assert.equal(await evaluate(`document.querySelectorAll('#timeline svg').length`),1);
        if(process.env.AMW_USAGE_SCREENSHOTS&&blocked&&[1440,390].includes(width)){const {data}=await page('Page.captureScreenshot',{format:'png',captureBeyondViewport:false});await writeFile(`${process.env.AMW_USAGE_SCREENSHOTS}/usage-${width===1440?'desktop':'mobile'}.png`,Buffer.from(data,'base64'));}
        const saved=await evaluate(`document.getElementById('cards').textContent.includes('6,176,181')`);
        if(saved){
          await choose('velero');const gap=await evaluate(`document.getElementById('account-values').textContent`);
          await dimension(2);
          assert.ok(await evaluate(`document.getElementById('rows').textContent.includes('918,182')&&document.getElementById('rows').textContent.includes('957,115')`),'Velero cluster totals');
          await choose('int-westus3-mgmt-1');await dimension(1);
          assert.ok(await evaluate(`document.getElementById('rows').textContent.includes('918,182')`));
          const chips=await evaluate(`document.getElementById('filters').textContent`);
          await evaluate(`document.querySelector('[data-mode="rate"]').click()`);await measure();
          assert.equal(await evaluate(`document.getElementById('filters').textContent`),chips);
          const expected=43538652/(8275/60);
          const actual=await evaluate(`Number(document.querySelector('#rows tr td:nth-child(2)').textContent.replaceAll(',',''))`);
          assert.ok(Math.abs(actual-expected)<.01,`rate ${actual} != ${expected}`);
          await evaluate(`document.querySelector('[data-mode="series"]').click()`);assert.equal(await evaluate(`document.getElementById('account-values').textContent`),gap);
          await reset();await dimension(2);await choose('int-westus3-mgmt-1');await dimension(1);await choose('velero');await dimension(2);
          assert.ok(await evaluate(`document.getElementById('rows').textContent.includes('918,182')`),'filter order changed totals');
          await reset();
        }
        for(const d of [0,1,2,3,4,5]){await dimension(d);await evaluate(`document.getElementById('next').click();document.getElementById('previous').click()`);await measure();assert.ok(await evaluate(`document.querySelectorAll('#rows tr').length<=10`));}
        await page('Page.bringToFront');await evaluate(`document.querySelector('[data-mode="rate"]').focus()`);await page('Input.dispatchKeyEvent',{type:'keyDown',key:'Enter',code:'Enter',text:'\r',windowsVirtualKeyCode:13});await page('Input.dispatchKeyEvent',{type:'keyUp',key:'Enter',code:'Enter',windowsVirtualKeyCode:13});assert.equal(await evaluate(`document.querySelector('[data-mode="rate"]').getAttribute('aria-pressed')`),'true');
        await evaluate(`document.getElementById('workspaces').lastElementChild.click()`);assert.equal(await evaluate(`document.getElementById('filters').children.length`),0);await evaluate(`document.getElementById('workspaces').firstElementChild.click()`);
        const enriched=await evaluate(`(()=>{const e=JSON.parse(document.getElementById('sample-data')?.textContent||'[]');return e.find(e=>e.Workspace==='services-westus3'&&e.Matched)?.Metric})()`);
         if(enriched){await dimension(0);await evaluate(`document.getElementById('search').value=${JSON.stringify(enriched)};document.getElementById('search').dispatchEvent(new Event('input'));Array.from(document.querySelectorAll('#rows tr')).find(r=>r.querySelector('button').textContent===${JSON.stringify(enriched)}).querySelector('.metric-labels').click()`);await measure();assert.ok(await evaluate(`document.getElementById('labels').open`));
           assert.ok(await evaluate(`document.getElementById('label-head').textContent.includes('Expansion')`));
          const before=await evaluate(`document.getElementById('label-scope').textContent`);await evaluate(`document.getElementById('label-window').selectedIndex=1;document.getElementById('label-window').dispatchEvent(new Event('change'))`);assert.notEqual(await evaluate(`document.getElementById('label-scope').textContent`),before);
          const length=await evaluate(`document.getElementById('label-choice').options.length`);
          for(let i=1;i<length;i++){await evaluate(`document.getElementById('label-choice').selectedIndex=${i};document.getElementById('label-choice').dispatchEvent(new Event('change'));document.getElementById('label-next').click()`);await measure();assert.ok(await evaluate(`document.querySelectorAll('#label-rows tr').length<=10`));}
          assert.ok(await evaluate(`Array.from(document.getElementById('label-choice').options).some(o=>o.textContent.includes(' + '))`),'missing pairs');await evaluate(`document.getElementById('close-labels').click()`);
        }
         assert.ok(await evaluate(`document.getElementById('map-toggle').closest('#explorer > .toolbar')!==null`));
         assert.ok(await evaluate(`document.getElementById('treemap-legend').hidden && document.querySelectorAll('#treemap-legend .legend-square').length===4`));
         assert.ok(await evaluate(`document.querySelectorAll('#account-values .legend-square').length===3 && !!document.querySelector('#timeline [data-period="Test run"]')`));
         if(saved) assert.ok(await evaluate(`!!document.querySelector('#timeline [data-period="Previous baseline"]')`));
         if(!blocked){await wait(`typeof window.echarts?.init==='function'`);assert.equal(await evaluate('echarts.version'),'5.6.0');await evaluate(`document.getElementById('map-toggle').click()`);assert.ok(await evaluate(`!!document.querySelector('#treemap canvas') && document.getElementById('table-view').hidden && document.getElementById('map-toggle').getAttribute('aria-pressed')==='true'`));await measure();await evaluate(`document.getElementById('table-toggle').click()`);assert.ok(await evaluate(`document.getElementById('treemap').hidden && !document.getElementById('table-view').hidden && document.getElementById('table-toggle').getAttribute('aria-pressed')==='true'`));}
         else assert.equal(await evaluate(`document.getElementById('map-toggle').disabled`),true);
         if(saved&&!blocked&&width===1440){
           await evaluate(`document.querySelector('[data-mode="series"]').click();Array.from(document.querySelectorAll('#workspaces button')).find(b=>b.textContent==='HCP').click()`);await dimension(1);
           const recovered=await evaluate(`(()=>{
             const d=JSON.parse(document.getElementById('budget-data').textContent),w=d.Workspaces.find(w=>w.Label==='HCP'),groups=new Map();
             for(const f of w.Facts){const g=groups.get(f[1])||{id:f[1],value:0,partial:0,mask:0};g.value+=d.Counts[f[6]][1];if(f[7]&2)g.partial+=d.Counts[f[6]][1];g.mask|=f[7];groups.set(f[1],g);}
             const rows=Array.from(groups.values()).sort((a,b)=>b.value-a.value),largest=rows.find(g=>g.id!==-1),metric=w.Metrics.find(id=>d.Strings[d.Metrics[id].n].startsWith('apiserver_')&&w.Facts.some(f=>f[0]===id&&(f[7]&2)&&d.Counts[f[6]][1]>0));
             return {total:rows.reduce((n,g)=>n+g.value,0),gap:w.Series.Residual,largest:{...largest,name:d.Strings[largest.id]},metric:metric==null?null:d.Strings[d.Metrics[metric].n],omitted:rows.slice(50).reduce((n,g)=>n+g.value,0)};
           })()`);
           assert.equal(recovered.total+recovered.gap,7229773,'HCP source facts plus fixed AMW gap');
           assert.ok(recovered.largest.partial>0,'largest known namespace includes recovered partitions');
           assert.ok(await evaluate(`document.getElementById('confidence').textContent.includes('Measured 7,024,208') && document.getElementById('account-values').textContent.includes('Unaccounted 205,565')`),'measured usage includes successful partial results');
           let total=0;
           do{total+=await evaluate(`Array.from(document.querySelectorAll('#rows tr')).reduce((n,r)=>n+Number(r.cells[1].firstChild.textContent.replaceAll(',','').replace('\u2265','')),0)`);if(await evaluate(`document.getElementById('next').disabled`))break;await evaluate(`document.getElementById('next').click()`);}while(true);
           assert.equal(total,recovered.total,'all paginated namespace rows match source facts');
           await evaluate(`document.getElementById('map-toggle').click()`);
           assert.ok(await evaluate(`!document.getElementById('treemap-legend').hidden && document.getElementById('treemap-legend').textContent.includes('Other: smaller groups combined')`));
           const tiles=await evaluate(`echarts.getInstanceByDom(document.getElementById('treemap')).getOption().series[0].data`);
           assert.equal(tiles.reduce((n,g)=>n+g.value,0),7229773,'HCP treemap including remainder and AMW gap');
           assert.ok(!tiles.some(g=>g.name.includes('source unknown')),'partial facts must not be counted twice');
           assert.ok(tiles.filter(g=>g.partial&&!g.name.startsWith('Other ')).every(g=>g.itemStyle.color==='#39958b'),'lower-bound measurements are green');
           assert.equal(tiles.find(g=>g.name==='Unaccounted / AMW').itemStyle.color,'#d49424','only unaccounted usage is orange');
           assert.equal(tiles.find(g=>g.name===recovered.largest.name).value,recovered.largest.value);
           assert.equal(tiles.find(g=>g.name==='Other namespaces').value,recovered.omitted);
           assert.equal(tiles.find(g=>g.name==='Other namespaces').partial,true);
           assert.ok(await evaluate(`(()=>{const s=echarts.getInstanceByDom(document.getElementById('treemap')).getOption().series[0],g=s.data.find(g=>g.name==='Other namespaces');return s.label.formatter({name:g.name,value:g.value,data:g}).includes('\\n\u2265')})()`));
           await evaluate(`document.getElementById('search').value=${JSON.stringify(recovered.largest.name)};document.getElementById('search').dispatchEvent(new Event('input'))`);
           assert.equal(await evaluate(`document.querySelector('#rows tr').cells[3].firstChild.textContent`),'Unknown','real namespace incomplete endpoints cannot bound growth');
           assert.equal(await evaluate(`document.querySelector('#rows tr').cells[3].title`),(recovered.largest.mask&3)===3?'Both endpoint totals are lower bounds; their difference does not bound net growth':'');
           assert.ok(recovered.metric,'recovered apiserver metric');await dimension(0);await choose(recovered.metric);
           assert.ok(await evaluate(`Array.from(document.querySelectorAll('#rows tr')).some(r=>r.cells[1].textContent.startsWith('\u2265')&&!r.cells[1].textContent.includes('Unknown'))`),'partial metric namespace drilldown retains counts');
           await evaluate(`document.getElementById('table-toggle').click()`);await reset();
         }
        assert.equal(await evaluate('document.documentElement.scrollWidth>innerWidth'),false);
      }
    }
  }
  // Synthetic label identities exercise the real UI without changing the saved
  // report or SQLite. Load with scripts paused, replace only the sample JSON,
  // then execute the report's own inline explorer script.
  await page('Network.setBlockedURLs',{urls:['*cdn.jsdelivr.net*']});
  await page('Emulation.setScriptExecutionDisabled',{value:true});
  await page('Page.navigate',{url:pathToFileURL(process.argv[2]).href});
  await wait(`document.readyState==='complete'&&!!document.getElementById('budget-data')`);
  const fixtureMetric=await evaluate(`(()=>{
    const data=JSON.parse(document.getElementById('budget-data').textContent),w=data.Workspaces.find(w=>w.Label==='Services')||data.Workspaces[0],m=data.Strings[data.Metrics[w.Metrics[0]].n];
    const values=[null,'','(absent)','(empty)'],weight={Series:1,Rate:1,Share:25};
    const e={Workspace:w.Name,Metric:m,Selector:m,Window:'identity fixture',Start:0,End:60,Minutes:1,Matched:true,Full:{Series:4,Rate:4},
      Labels:[{Name:'a',Distinct:3,Physical:4,BaseIdentities:1,Reduction:3,Values:values.map(Value=>({Value,...weight}))}],
      LabelPairs:[{Names:['a','b'],Distinct:4,BaseIdentities:1,Reduction:3,Values:values.map((v,i)=>({Values:[v,values[3-i]],...weight}))}]};
    let sample=document.getElementById('sample-data');if(!sample){sample=document.createElement('script');sample.id='sample-data';sample.type='application/json';document.body.append(sample);}sample.textContent=JSON.stringify([e]);return m;
  })()`);
  const inline=await evaluate(`Array.from(document.scripts).find(s=>!s.src&&s.type!=='application/json').textContent`);
  await page('Emulation.setScriptExecutionDisabled',{value:false});await evaluate(inline);
  await dimension(0);await choose(fixtureMetric);await evaluate(`document.getElementById('inspect').click()`);
  assert.equal(await evaluate(`Array.from(document.querySelectorAll('#label-rows tr')).find(r=>r.cells[0].textContent==='a + b').cells[1].textContent`),'4','pair distinct count');
  await evaluate(`Array.from(document.querySelectorAll('#label-rows button')).find(b=>b.textContent==='a').click()`);
  assert.deepEqual(await evaluate(`Array.from(document.querySelectorAll('#label-rows tr'),r=>r.cells[0].textContent)`),['(absent)','(empty)','"(absent)"','"(empty)"']);
  await evaluate(`document.getElementById('label-back').click();Array.from(document.querySelectorAll('#label-rows button')).find(b=>b.textContent==='a + b').click()`);
  assert.deepEqual(await evaluate(`Array.from(document.querySelectorAll('#label-rows tr'),r=>r.cells[0].textContent)`),['a=(absent) / b="(empty)"','a=(empty) / b="(absent)"','a="(absent)" / b=(empty)','a="(empty)" / b=(absent)']);
  await measure();
  // Packed source facts cover partial zeros, mixed parents and absent filtered
  // observations. This fixture changes only in-memory JSON, never SQLite.
  await page('Emulation.setScriptExecutionDisabled',{value:true});
  await page('Page.navigate',{url:pathToFileURL(process.argv[2]).href});
  await wait(`document.readyState==='complete'&&!!document.getElementById('budget-data')`);
  await evaluate(`(()=>{
    const d=JSON.parse(document.getElementById('budget-data').textContent),w=d.Workspaces[0];
    d.Workspaces=[w];d.Strings=[];d.Counts=[];d.Metrics=[];d.Minutes=2;w.Facts=[];w.Metrics=[];w.ActiveEnd=10000;w.Mean=10000;
    const intern=s=>{let i=d.Strings.indexOf(s);if(i<0){i=d.Strings.length;d.Strings.push(s);}return i;};
    const metric=(name,Before,End,Samples,lower={})=>{const id=d.Metrics.length;d.Metrics.push({n:intern(name),Before,End,Samples,...lower});w.Metrics.push(id);return id;};
    const fact=(id,namespace,counts,mask=0)=>{const n=namespace==null?-1:intern(namespace);w.Facts.push([id,n,n,n,n,n,d.Counts.length,mask]);d.Counts.push(counts);};
    const complete=metric('complete',102,204,408),partial=metric('partial',null,null,null,{LowerBefore:0,LowerEnd:10,LowerSamples:20});
    fact(complete,'mixed',[100,200,400]);fact(partial,'mixed',[0,10,20],7);fact(partial,'before-only-source',[0,0,0],1);
    fact(complete,null,[1,2,4]);fact(complete,'complete-only',[1,2,4]);
    const before=metric('partial-before',null,7,14,{LowerBefore:0});fact(before,'before-zero',[0,7,14],1);
    const end=metric('partial-end',4,null,16,{LowerEnd:8});fact(end,'end-partial',[4,8,16],2);
    const samples=metric('partial-samples',2,3,null,{LowerSamples:6});fact(samples,'samples-partial',[2,3,6],4);
    const missing=metric('missing-period',null,9,null);fact(missing,'missing-before',[0,9,0]);
    const failed=metric('failed',null,null,null);fact(failed,'failed-source',[0,0,0]);
    const tiny=metric('tiny-partial',null,null,null,{LowerBefore:0,LowerEnd:1,LowerSamples:2});fact(tiny,'tiny',[0,1,2],7);
    metric('partial-zero',null,null,null,{LowerBefore:0,LowerEnd:0,LowerSamples:0});
    metric('complete-empty',0,0,0);
    const negative=metric('negative-bound',10,null,16,{LowerEnd:8});fact(negative,'negative-partial',[10,8,16],2);
    const missingEnd=metric('missing-end',4,null,null);fact(missingEnd,'no-end',[4,0,0]);
    for(let i=0;i<55;i++){const id=metric('filler-'+i,20,30+i,60+2*i);fact(id,'namespace-'+i,[20,30+i,60+2*i]);}
    w.IncompleteSeries=4;w.IncompleteRate=5;
    w.Series={...w.Series,Complete:3400,Partial:19,Residual:23};w.Rate={...w.Rate,Complete:3400,Partial:14,Residual:29};
    document.getElementById('budget-data').textContent=JSON.stringify(d);
  })()`);
  const sourceInline=await evaluate(`Array.from(document.scripts).find(s=>!s.src&&s.type!=='application/json').textContent`);
  await page('Emulation.setScriptExecutionDisabled',{value:false});
  // Capture the actual treemap option offline; the report above exercises ECharts.
  await evaluate(`window.echarts={init:()=>({on(event,handler){window.mapClick=handler;},setOption(o){window.mapOption=o;},resize(){}})}`);
  await evaluate(sourceInline);
  const row=async name=>evaluate(`(()=>{document.getElementById('search').value=${JSON.stringify(name)};document.getElementById('search').dispatchEvent(new Event('input'));const r=Array.from(document.querySelectorAll('#rows tr')).find(r=>r.cells[0].querySelector('button')?.textContent===${JSON.stringify(name)});return r?Array.from(r.cells,c=>c.firstChild.textContent):null})()`);
  assert.equal((await row('mixed'))[1],'\u2265210','complete plus partial namespace');
  assert.equal((await row('mixed'))[3],'Unknown','never difference partial lower bounds');
  assert.equal(await evaluate(`document.querySelector('#rows tr').cells[3].title`),'Both endpoint totals are lower bounds; their difference does not bound net growth');
  assert.equal((await row('end-partial'))[3],'Unknown','global missing before periods cannot establish an exact baseline');
  assert.equal((await row('(absent)'))[1],'2','missing namespace is a real row');
  assert.equal((await row('failed-source'))[1],'0','complete global parents can establish zero');
  await evaluate(`document.getElementById('map-toggle').click()`);
  const syntheticGap=await evaluate(`document.getElementById('account-values').textContent`);
  for(const [d,name] of ['metrics','namespaces','clusters','scrape jobs','HCP labels','Prometheus sources'].entries()){
    await dimension(d);
    const other=await evaluate(`mapOption.series[0].data.find(g=>g.name===${JSON.stringify('Other '+name)})`);
    assert.ok(other?.value>0,`Other ${name} exact omitted sum`);assert.equal(other.partial,true);assert.equal(other.itemStyle.color,'#647b8a');
    assert.ok(await evaluate(`(()=>{const s=mapOption.series[0],g=s.data.find(g=>g.name===${JSON.stringify('Other '+name)});return s.label.formatter({name:g.name,value:g.value,data:g}).includes('\\n\u2265')})()`));
    assert.ok(await evaluate(`!mapOption.series[0].data.some(g=>g.name.includes('source unknown'))`));
  }
  await dimension(0);
  for(const [name,value,growth] of [['partial','\u226510','Unknown'],['partial-before','7','\u22647'],['partial-end','\u22658','\u22654'],['negative-bound','\u22658','\u2265-2'],['missing-period','9','Unknown'],['missing-end','Unknown','Unknown'],['failed','Unknown','Unknown'],['partial-zero','\u22650','Unknown'],['complete-empty','0','0'],['complete','204','+102']]){const r=await row(name);assert.equal(r[1],value,name);assert.equal(r[3],growth,name+' growth');}
  await row('partial-before');
  assert.equal(await evaluate(`document.querySelector('#rows tr').cells[3].querySelector('small').textContent`),'Before \u22650','a measured zero lower bound is not an exact baseline');
  assert.ok(await evaluate(`document.getElementById('scope').textContent.includes('\u2265 denotes a lower bound')&&!document.getElementById('explorer').textContent.includes('>=')`));
  assert.equal((await row('partial-samples'))[3],'+1','sample partial does not invalidate complete series growth');
  await choose('partial');
  assert.equal((await row('mixed'))[1],'\u226510','fully partial metric namespace remains available');
  assert.equal((await row('mixed'))[3],'Unknown','partial zero baseline blocks growth');
  assert.equal((await row('before-only-source'))[1],'Unknown','no end observations cannot fabricate zero');
  await choose('mixed');await dimension(0);
  assert.equal((await row('partial'))[1],'\u226510','source-filtered metric with null End uses partial facts');
  await evaluate(`document.querySelector('[data-mode="rate"]').click()`);
  assert.equal((await row('partial'))[1],'\u226510','partial samples divided by run minutes');
  await reset();await dimension(1);await choose('complete-only');await dimension(0);
  // Select a metric outside this source without inventing a matching fact.
  await evaluate(`document.querySelector('[data-mode="series"]').click();document.getElementById('search').value='';document.getElementById('search').dispatchEvent(new Event('input'))`);
  await choose('complete');
  assert.equal((await row('complete-only'))[3],'+1','selected complete metric stays comparable despite other missing parents');
  assert.equal(await evaluate(`document.getElementById('account-values').textContent`),syntheticGap,'source filters never alter AMW gap');
  // Exercise an absent selected metric through the same handler as a tile click.
  await reset();await dimension(1);await choose('complete-only');await dimension(0);
  await evaluate(`mapClick({data:{id:1}})`);await dimension(0);
  assert.equal((await row('partial'))[1],'Unknown','absent fully partial selected metric is not zero');
  assert.equal((await row('partial'))[3],'Unknown');
  await evaluate(`document.querySelector('[data-mode="rate"]').click()`);
  assert.equal((await row('partial'))[1],'Unknown','absent partial samples are not zero');
  await evaluate(`document.querySelector('[data-mode="series"]').click()`);
  await reset();await dimension(0);await choose('failed');
  assert.equal((await row('failed-source'))[1],'Unknown','failed parent cannot establish a source zero');
  assert.equal((await row('failed-source'))[3],'Unknown','failed parent cannot establish growth');
  for(const [name,namespace,value,growth] of [['partial-before','before-zero','7','\u22647'],['partial-end','end-partial','\u22658','\u22654'],['negative-bound','negative-partial','\u22658','\u2265-2'],['partial-samples','samples-partial','3','+1'],['missing-period','missing-before','9','Unknown'],['missing-end','no-end','Unknown','Unknown']]){
    await reset();await dimension(0);await choose(name);
    for(const d of [1,2,3,4,5]){await dimension(d);const r=await row(namespace);assert.equal(r[1],value,name+' grouped value');assert.equal(r[3],growth,name+' grouped growth');}
    await dimension(1);await choose(namespace);await dimension(0);
    const r=await row(name);assert.equal(r[1],value,name+' filtered value');assert.equal(r[3],growth,name+' filtered growth');
    if(name==='partial-samples'){await evaluate(`document.querySelector('[data-mode="rate"]').click()`);assert.equal((await row(name))[1],'\u22653');await evaluate(`document.querySelector('[data-mode="series"]').click()`);}
  }
  await reset();await dimension(1);await choose('complete-only');await dimension(0);
  const emptyID=await evaluate(`(()=>{const d=JSON.parse(document.getElementById('budget-data').textContent);return d.Metrics.findIndex(m=>d.Strings[m.n]==='complete-empty')})()`);
  await evaluate(`mapClick({data:{id:${emptyID}}})`);await dimension(0);
  assert.equal((await row('complete-empty'))[1],'0','complete empty selected metric remains zero');
  assert.equal((await row('complete-empty'))[3],'0');
  // Cross-metric bounds require every parent on the exact side to be complete,
  // not merely an unmarked source fact or some complete parents.
  const packedFixture=await evaluate(`document.getElementById('budget-data').textContent`);
  for(const [metric,growth] of [['partial-end','\u2265106'],['partial-before','\u2264109'],['partial','Unknown'],['missing-period','Unknown']]){
    await page('Emulation.setScriptExecutionDisabled',{value:true});
    await page('Page.navigate',{url:pathToFileURL(process.argv[2]).href});
    await wait(`document.readyState==='complete'&&!!document.getElementById('budget-data')`);
    await evaluate(`(()=>{
      const d=JSON.parse(${JSON.stringify(packedFixture)}),w=d.Workspaces[0],names=['complete',${JSON.stringify(metric)}];
      w.Metrics=w.Metrics.filter(id=>names.includes(d.Strings[d.Metrics[id].n]));
      const n=d.Strings.length;d.Strings.push('combined');
      w.Facts=w.Facts.filter(f=>w.Metrics.includes(f[0])).map(f=>[f[0],n,n,n,n,n,f[6],f[7]]);
      document.getElementById('budget-data').textContent=JSON.stringify(d);
    })()`);
    await page('Emulation.setScriptExecutionDisabled',{value:false});await evaluate(sourceInline);
    for(const d of [1,2,3,4,5]){await dimension(d);assert.equal((await row('combined'))[3],growth,metric+' cross-metric growth');}
  }
  await measure();
  assert.deepEqual(errors,[]);console.log(JSON.stringify({initialDOM,maxDOM,maxInteractionMs:maxMs}));console.log('Usage explorer passed: 1440/390/320, offline/no-JS/real ECharts, exact crossfilters, paging, keyboard, label identities, recovered namespace reconciliation, partial masks and unknown periods.');
}finally{socket?.close();chrome.kill('SIGKILL');}
