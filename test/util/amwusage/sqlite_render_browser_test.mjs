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

// Legacy report check: node sqlite_render_browser_test.mjs /path/to/legacy-report.html
import {spawn} from 'node:child_process';
import assert from 'node:assert/strict';
import {pathToFileURL} from 'node:url';
import {readFileSync} from 'node:fs';

const asset = process.env.AMW_ECHARTS_ASSET;
const library = asset ? readFileSync(asset).toString('base64') : null;
const fullscan = !!process.env.AMW_DATABASE_FULLSCAN;
const partial = !!process.env.AMW_DATABASE_PARTIAL;

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
  const pending = new Map(), errors = [];
  let intercepted = 0;
  socket.addEventListener('message', event => {
    const msg = JSON.parse(event.data);
    if (msg.method === 'Runtime.exceptionThrown') errors.push(msg.params);
    if (msg.method === 'Fetch.requestPaused') {
      intercepted++;
      call('Fetch.fulfillRequest',{requestId:msg.params.requestId,responseCode:200,responseHeaders:[{name:'Content-Type',value:'application/javascript'}],body:library},msg.sessionId).catch(error=>errors.push(String(error)));
    }
    if (msg.id) { const p=pending.get(msg.id); pending.delete(msg.id); msg.error ? p.reject(Error(JSON.stringify(msg.error))) : p.resolve(msg.result); }
  });
  const call = (method,params={},sessionId) => new Promise((resolve,reject) => {pending.set(++id,{resolve,reject}); socket.send(JSON.stringify({id,method,params,sessionId}));});
  const {targetId}=await call('Target.createTarget',{url:'about:blank'});
  const {sessionId}=await call('Target.attachToTarget',{targetId,flatten:true});
  const page=(method,params)=>call(method,params,sessionId);
  const evaluate=async expression=>{const result=await page('Runtime.evaluate',{expression,returnByValue:true,awaitPromise:true}); if(result.exceptionDetails) throw Error(JSON.stringify(result.exceptionDetails)); return result.result.value;};
  await page('Runtime.enable'); await page('Network.enable');
  await page('Network.setBlockedURLs',{urls:['http://*','https://*']});
  for (const mode of (library ? ['offline','nojs','real'] : ['offline','nojs'])) {
    const nojs = mode==='nojs';
    await page('Network.setBlockedURLs',{urls:mode==='real' ? [] : ['http://*','https://*']});
    if (mode==='real') await page('Fetch.enable',{patterns:[{urlPattern:'https://cdn.jsdelivr.net/npm/echarts@5.6.0/dist/echarts.min.js',requestStage:'Request'}]});
    await page('Emulation.setScriptExecutionDisabled',{value:nojs});
    for (const width of [1440,390,320]) {
      await page('Emulation.setDeviceMetricsOverride',{width,height:900,deviceScaleFactor:1,mobile:width<700});
      await page('Page.navigate',{url:pathToFileURL(process.argv[2]).href});
      for (let i=0;i<200;i++) {
        if(await evaluate(`document.readyState==='complete' && !!document.querySelector('#db-metrics tbody tr')`)) break;
        await new Promise(r=>setTimeout(r,50));
      }
      assert.equal(await evaluate('document.documentElement.scrollWidth>innerWidth'),false,`overflow at ${width}, nojs=${nojs}`);
      const count=await evaluate(`document.querySelectorAll('#db-metrics tbody tr').length`);
	  const reconciliation=await evaluate(`({periods:document.querySelectorAll('.db-reconciliation-period').length,bars:document.querySelectorAll('.db-reconciliation-bar').length,text:document.getElementById('db-reconciliation').textContent,share:document.querySelector('#db-metrics .db-amw-share').textContent})`);
	  assert.equal(reconciliation.periods,fullscan ? 4 : 2);
	  assert.equal(reconciliation.bars,partial ? 0 : (fullscan ? 4 : 2));
	  assert.ok(reconciliation.text.includes('Before the run') && reconciliation.text.includes('At run end') && reconciliation.text.includes('Unaccounted / reconciliation gap'));
	  if(!fullscan && !partial) { assert.equal(reconciliation.share,'7.50%'); assert.ok(reconciliation.text.includes('92.50%')); }
      assert.ok(fullscan ? count>3000 : count===2);
      assert.equal(await evaluate(`document.getElementById('db-treemap').hidden`),mode!=='real');
      if (!fullscan) assert.equal(await evaluate(`document.querySelector('#db-metrics tbody td').textContent`),'test');
      if(nojs) continue;
      assert.ok(await evaluate(`document.querySelectorAll('#db-source-table tbody tr').length>0`));
      if (!fullscan && !partial) {
      assert.ok(await evaluate(`document.getElementById('db-source-table').textContent.includes('(absent)') && document.getElementById('db-source-table').textContent.includes('(empty)')`));
      assert.equal(await evaluate(`(() => {const f=document.getElementById('db-filter'); f.value='other'; f.dispatchEvent(new Event('input')); return [...document.querySelectorAll('#db-metrics tbody tr')].filter(r=>!r.hidden).length;})()`),1);
      assert.equal(await evaluate(`(() => {document.querySelectorAll('.db-select')[1].click(); return document.getElementById('db-selection').textContent;})()`),'test / other');
      await evaluate(`(() => {const f=document.getElementById('db-filter');f.value=''; f.dispatchEvent(new Event('input')); document.querySelector('[data-sort="Before"]').click();})()`);
      assert.equal(await evaluate(`document.querySelector('#db-metrics tbody .db-select').textContent`),'up');
      await evaluate(`document.querySelector('[data-sort="Before"]').click()`);
      assert.equal(await evaluate(`document.querySelector('#db-metrics tbody .db-select').textContent`),'other');
      }
      if (partial) {
        assert.equal(await evaluate(`document.getElementById('db-lower-bounds').checked`),false);
        assert.ok(await evaluate(`document.getElementById('db-metrics').textContent.includes('>=0 (partial; total unknown)')`));
        await evaluate(`document.getElementById('db-weight').value='Before'; document.getElementById('db-weight').dispatchEvent(new Event('change'));`);
        if(mode==='real') assert.equal(await evaluate(`echarts.getInstanceByDom(document.getElementById('db-treemap')).getOption().series[0].data.length`),0);
        await evaluate(`document.getElementById('db-lower-bounds').click()`);
        if(mode==='real') {
          const bounds=await evaluate(`echarts.getInstanceByDom(document.getElementById('db-treemap')).getOption().series[0].data.flatMap(w=>w.children).map(m=>({value:m.value,partial:m.partial,name:m.name}))`);
          assert.deepEqual(bounds,[{value:10,partial:true,name:'up (partial lower bound)'}]);
        }
        await evaluate(`document.getElementById('db-weight').value='Delta'; document.getElementById('db-weight').dispatchEvent(new Event('change'));`);
        if(mode==='real') assert.equal(await evaluate(`echarts.getInstanceByDom(document.getElementById('db-treemap')).getOption().series[0].data.length`),0);
        await evaluate(`document.getElementById('db-lower-bounds').click(); document.getElementById('db-weight').value='Samples'; document.getElementById('db-weight').dispatchEvent(new Event('change'));`);
      }
      if (mode==='real') {
        assert.equal(await evaluate(`echarts.version`),'5.6.0');
        const state=await evaluate(`(() => {const chart=echarts.getInstanceByDom(document.getElementById('db-treemap'));const series=chart.getOption().series[0]; const leaf=series.data.flatMap(w=>w.children)[0]; chart.trigger('click',{data:leaf}); return {canvas:!!document.querySelector('#db-treemap canvas'),leaves:series.data.flatMap(w=>w.children).length,selection:document.getElementById('db-selection').textContent,metric:leaf.name};})()`);
        assert.ok(state.canvas && state.leaves>0);
        assert.ok(state.selection.endsWith(' / '+state.metric));
        await evaluate(`document.getElementById('db-weight').value='Samples'; document.getElementById('db-weight').dispatchEvent(new Event('change')); window.dispatchEvent(new Event('resize'));`);
        assert.ok(await evaluate(`echarts.getInstanceByDom(document.getElementById('db-treemap')).getOption().series[0].data.length>0`));
      }
      assert.equal(await evaluate(`document.documentElement.scrollWidth>innerWidth`),false);
	  assert.equal(await evaluate(`document.getElementById('db-reconciliation').textContent`),reconciliation.text,'filtering and checkbox must not change AMW reconciliation');
    }
  }
  assert.deepEqual(errors,[]);
  if (library) assert.equal(intercepted,3);
  console.log(`Database report: desktop/mobile, offline/no-JS, source drilldown${fullscan ? '' : ', filtering and sorting'}${library ? ', real ECharts 5.6.0 (intercepted CDN asset)' : ''} passed.`);
} finally {
  if(socket) socket.close();
  chrome.kill('SIGKILL');
}
