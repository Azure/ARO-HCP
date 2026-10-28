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

// Optional browser regression: node contribution_browser_test.mjs /path/to/report.html
// Requires Chrome and Node 22+. Uses the rendered evidence, never fabricates measurements.
import {spawn} from 'node:child_process';
import assert from 'node:assert/strict';
import {pathToFileURL} from 'node:url';

const report = pathToFileURL(process.argv[2]).href;
const chrome = spawn(process.env.CHROME || '/usr/bin/google-chrome', ['--headless', '--no-sandbox', '--disable-gpu', '--disable-background-networking', '--no-first-run', '--no-default-browser-check', '--remote-debugging-port=0', 'about:blank']);
let socket;
try {
  const endpoint = await new Promise((resolve,reject) => {
    let stderr = '';
    const timer = setTimeout(() => reject(Error('Chrome startup timeout')), 20000);
    chrome.stderr.on('data', chunk => {stderr += chunk; const match = stderr.match(/DevTools listening on (ws:\/\/[^\s]+)/); if (match) {clearTimeout(timer); resolve(match[1]);}});
    chrome.on('error',reject);
  });
  socket = new WebSocket(endpoint);
  await new Promise(resolve => socket.addEventListener('open',resolve,{once:true}));
  let id = 0;
  const pending = new Map(), errors = [], network = [];
  socket.addEventListener('message',event => {
    const msg = JSON.parse(event.data);
    if (msg.method === 'Runtime.exceptionThrown') errors.push(msg.params);
    if (msg.method === 'Network.requestWillBeSent') network.push(msg.params.request.url);
    if (msg.id) {const p = pending.get(msg.id); pending.delete(msg.id); msg.error ? p.reject(Error(JSON.stringify(msg.error))) : p.resolve(msg.result);}
  });
  const call = (method,params = {},sessionId) => new Promise((resolve,reject) => {pending.set(++id,{resolve,reject});socket.send(JSON.stringify({id,method,params,sessionId}));});
  const {targetId} = await call('Target.createTarget',{url:'about:blank'});
  const {sessionId} = await call('Target.attachToTarget',{targetId,flatten:true});
  const page = (method,params) => call(method,params,sessionId);
  const evaluate = async expression => {const result = await page('Runtime.evaluate',{expression,returnByValue:true,awaitPromise:true});if (result.exceptionDetails) throw Error(JSON.stringify(result.exceptionDetails));return result.result.value;};
  const select = (id,value) => evaluate(`(() => {const el = document.getElementById(${JSON.stringify(id)}); el.value = ${JSON.stringify(value)}; el.dispatchEvent(new Event('change'));})()`);
  await page('Runtime.enable'); await page('Network.enable');
  const results = [];
  for (const mode of (process.env.AMW_BROWSER_OFFLINE ? ['offline','nojs'] : ['offline','cdn','nojs'])) {
    await page('Network.setBlockedURLs',{urls: mode === 'cdn' ? [] : ['http://*','https://*']});
    await page('Emulation.setScriptExecutionDisabled',{value: mode === 'nojs'});
    for (const width of [1440,390,320]) {
      await page('Emulation.setDeviceMetricsOverride',{width,height:900,deviceScaleFactor:1,mobile:width < 700});
      await page('Page.navigate',{url:report});
      for (let i = 0; i < 200; i++) {
        if (await evaluate(`!!document.querySelector('#contribution-table tbody tr') && (document.readyState === 'complete')`)) break;
        await new Promise(r => setTimeout(r,50));
      }
      const state = await evaluate(`({overflow:document.documentElement.scrollWidth > innerWidth,rows:document.querySelectorAll('#contribution-table tbody tr').length,chart:!!window.echarts && !!echarts.getInstanceByDom(document.getElementById('contribution-treemap')),status:document.getElementById('treemap-status').textContent,summary:document.getElementById('contribution-summary').textContent})`);
      assert.equal(state.overflow,false); assert.ok(state.rows > 0); assert.equal(state.chart,false);
      assert.equal(await evaluate(`document.querySelector('h1').textContent`),'Where did the active series come from?');
      assert.equal(await evaluate(`document.getElementById('sample-comparison').open`),false);
      const inventory = await evaluate(`(() => {const m=JSON.parse(document.getElementById('active-data').textContent);return {count:m.rows.length,chart:!!window.echarts && !!echarts.getInstanceByDom(document.getElementById('active-treemap')),kpis:document.querySelector('.active-kpis').textContent};})()`);
      assert.equal(inventory.chart,mode === 'cdn');
      if (mode !== 'nojs') {
        for (const grouping of ['source','metric','collector']) {
          await select('active-group',grouping);
          for (const period of ['before','growth','end']) {
            await evaluate(`document.querySelector('[data-period="${period}"]').click()`);
            const check=await evaluate(`(() => {const m=JSON.parse(document.getElementById('active-data').textContent);const period=${JSON.stringify(period)};const n=m.rows.filter(r=>period==='before'?r.before:period==='end'?r.end:r.end&&!r.before).length;return {n,text:document.getElementById('active-selection').textContent,overflow:document.documentElement.scrollWidth>innerWidth};})()`);
            assert.ok(check.text.includes(check.n.toLocaleString())); assert.equal(check.overflow,false);
          }
        }
        await select('active-group','metric');
        if (inventory.count) {
          assert.equal(await evaluate(`(() => {document.querySelector('#active-ranked tbody button').click();return !document.getElementById('active-label-detail').hidden;})()`),true);
          const projection=await evaluate(`(() => {const rows=[...document.querySelectorAll('#active-labels tbody tr')];const replica=rows.find(r=>r.cells[0].textContent==='prometheus_replica');const le=rows.find(r=>r.cells[0].textContent==='le');const ns=rows.find(r=>r.cells[0].textContent==='namespace');return {replica:replica?.cells[4].textContent,le:le?.cells[4].textContent,namespace:ns?.cells[4].textContent,agents:document.getElementById('active-agents').textContent};})()`);
          if(inventory.count===13408){assert.equal(projection.replica,'5,952');assert.equal(projection.le,'11,408');assert.equal(projection.namespace,'0');assert.ok(projection.agents.includes('prom-agent-prometheus-shard-3-1'));assert.ok((await evaluate(`document.getElementById('active-shard-overlap').textContent`)).startsWith('0 replica-free identities and 0 target tuples'));}
          await evaluate(`document.querySelector('#active-labels tbody button').click()`);
          assert.ok(await evaluate(`document.querySelectorAll('#active-label-values progress').length>0`));
          await evaluate(`document.querySelector('#active-breadcrumb button').click()`);
          assert.equal(await evaluate(`document.getElementById('active-label-detail').hidden`),true);
          await select('active-workspace','0');
          assert.equal(await evaluate(`document.documentElement.scrollWidth>innerWidth`),false);
          await select('active-workspace','');
          // Exercise the actual collector hierarchy, not just the detail table.
          const agentGroups = await evaluate(`(() => {const m=JSON.parse(document.getElementById('active-data').textContent);const groups=new Map();for(const r of m.rows.filter(r=>r.end===true)){const key=JSON.stringify([r.workspace,r.collector,r.labels.cluster||'(absent)']);if(!groups.has(key))groups.set(key,{workspace:m.workspaceNames[r.workspace],collector:r.collector,cluster:r.labels.cluster||'(absent)',agents:{}});const g=groups.get(key),agent=r.labels.prometheus_replica||'(absent)';g.agents[agent]=(g.agents[agent]||0)+1;}return [...groups.values()];})()`);
          for(const expected of agentGroups){
            await select('active-group','collector');
            for(const name of [expected.workspace,expected.collector,expected.cluster]){
              assert.equal(await evaluate(`(() => {const b=[...document.querySelectorAll('#active-ranked tbody button')].find(b=>b.textContent===${JSON.stringify(name)});if(!b)return false;b.click();return true;})()`),true);
            }
            const actual=await evaluate(`Object.fromEntries([...document.querySelectorAll('#active-ranked tbody tr')].map(r=>[r.cells[0].textContent,Number(r.cells[1].textContent.replaceAll(',',''))]))`);
            assert.deepEqual(actual,expected.agents);
            await evaluate(`document.querySelector('#active-ranked tbody button').click()`);
            assert.equal(await evaluate(`(() => {document.querySelector('#active-ranked tbody button').click();return document.getElementById('active-breadcrumb').textContent.includes('Scrape job:');})()`),true);
          }
        }
        await evaluate(`document.getElementById('sample-comparison').open=true`);
        await new Promise(r=>setTimeout(r,100));
        assert.equal(await evaluate(`!!window.echarts && !!echarts.getInstanceByDom(document.getElementById('contribution-treemap'))`),mode==='cdn');
        assert.equal(await evaluate(`(() => {document.querySelector('#contribution-table tbody button').click();return document.getElementById('contribution-breadcrumb').textContent.includes('Back');})()`),true);
        assert.equal(await evaluate(`(() => {document.querySelector('#contribution-breadcrumb button').click();return !document.getElementById('contribution-breadcrumb').textContent.includes('Back');})()`),true);
        for (const group of ['source','metric','collector','customer','cohort']) {
          await select('contribution-group',group);
          for (const measure of ['samples','baselineSamples','baselineSeries','runSeries','newSeries']) {
            await select('contribution-weight',measure);
            const totals = await evaluate(`(() => {const m = JSON.parse(document.getElementById('contribution-data').textContent); const values = m.rows.map(r => r[${JSON.stringify(measure)}]).filter(v => v !== null);return {total:values.length ? values.reduce((a,b) => a+b,0) : null,summary:document.getElementById('contribution-summary').textContent,overflow:document.documentElement.scrollWidth > innerWidth,shares:[...document.querySelectorAll('#contribution-table tbody tr')].map(r => r.cells[8]?.textContent)};})()`);
            assert.equal(totals.overflow,false);
            assert.ok(totals.summary.includes(totals.total === null ? 'Unknown' : totals.total.toLocaleString(undefined,{maximumFractionDigits:1})));
            if (totals.total > 0) assert.ok(totals.shares.some(s => s.endsWith('/ 100.00%')));
          }
        }
        await select('contribution-group','customer');
        for (const scope of ['Run-owned customer cluster','Other HCP / ownership unknown','Shared / unattributed','Ambiguous']) {
          await select('contribution-suite',scope);
          await select('contribution-weight','runSeries');
          const check = await evaluate(`(() => {const m = JSON.parse(document.getElementById('contribution-data').textContent);const rows = m.rows.filter(r => r.ownership === ${JSON.stringify(scope)}); const values = rows.map(r => r.runSeries).filter(v => v !== null); return {total:values.length ? values.reduce((a,b) => a+b,0) : null,summary:document.getElementById('contribution-summary').textContent,customerCount:new Set(rows.map(r => r.customerID || r.ownership)).size,tableCount:document.querySelectorAll('#contribution-table tbody button').length};})()`);
          assert.ok(check.summary.includes(check.total === null ? 'Unknown' : check.total.toLocaleString(undefined,{maximumFractionDigits:1})));
          assert.equal(check.customerCount,check.tableCount);
          const rates = await evaluate(`(() => {const m = JSON.parse(document.getElementById('contribution-data').textContent);const rows = m.rows.filter(r => r.ownership === ${JSON.stringify(scope)});const values = rows.map(r => r.rateDelta).filter(v => v !== null);return {delta:values.length ? values.reduce((a,b) => a+b,0) : null,text:document.getElementById('contribution-rates').textContent};})()`);
          const signed = rates.delta === null ? 'Unknown' : (rates.delta > 0 ? '+' : '') + rates.delta.toLocaleString(undefined,{maximumFractionDigits:1});
          assert.ok(rates.text.includes('Paired rate delta: ' + signed));
          if (check.tableCount) {
            assert.equal(await evaluate(`(() => {document.querySelector('#contribution-table tbody button').click();return document.getElementById('contribution-breadcrumb').textContent.includes('Customer cluster:');})()`),true);
            await evaluate(`document.querySelector('#contribution-breadcrumb button').click()`);
          }
        }
        await select('contribution-suite','');
        for (const cohort of ['Group present in both periods','Group present only in baseline','Group present only in run','Period presence unknown']) {
          await select('contribution-cohort',cohort);
          assert.equal(await evaluate(`document.documentElement.scrollWidth > innerWidth`),false);
        }
        await select('contribution-cohort','');
        await select('contribution-workspace','0');
        assert.equal(await evaluate(`document.documentElement.scrollWidth > innerWidth`),false);
      }
      results.push({mode,width,overflow:state.overflow,activeChart:inventory.chart,inspectedUnion:inventory.count,headline:inventory.kpis.replace(/\s+/g,' ').trim(),secondaryChartInitiallyDeferred:!state.chart});
    }
  }
  const evidence = await evaluate(`(() => {const m=JSON.parse(document.getElementById('contribution-data').textContent);return {baseline:m.baselineValid,duration:m.baselineDuration,warnings:m.warnings,owned:m.rows.filter(r=>r.ownership==='Run-owned customer cluster').length,customerIDs:[...new Set(m.rows.map(r=>r.customerID).filter(Boolean))].length,scopes:document.getElementById('ownership-totals').innerText};})()`);
  assert.deepEqual(errors,[]);
  assert.deepEqual([...new Set(network.filter(url => !url.startsWith('file:')))],['https://cdn.jsdelivr.net/npm/echarts@5.6.0/dist/echarts.min.js']);
  console.log(JSON.stringify({results,errors,evidence},null,2));
} finally {socket?.close();chrome.kill();}
