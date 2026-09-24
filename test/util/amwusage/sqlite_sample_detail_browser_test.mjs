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

// node sqlite_sample_detail_browser_test.mjs /path/to/events-analysis.html
import {spawn} from 'node:child_process';
import assert from 'node:assert/strict';
import {pathToFileURL} from 'node:url';
import {readFileSync} from 'node:fs';

const asset = process.env.AMW_ECHARTS_ASSET;
const chrome = spawn(process.env.CHROME || '/usr/bin/google-chrome', ['--headless', '--no-sandbox', '--disable-gpu', '--disable-background-networking', '--no-first-run', '--no-default-browser-check', '--remote-debugging-port=0', 'about:blank']);
let socket;
try {
  const endpoint = await new Promise((resolve, reject) => {
    let stderr = '';
    const timer = setTimeout(() => reject(Error('Chrome startup timeout')), 20000);
    chrome.stderr.on('data', chunk => { stderr += chunk; const m = stderr.match(/DevTools listening on (ws:\/\/[^\s]+)/); if (m) { clearTimeout(timer); resolve(m[1]); } });
    chrome.on('error', reject);
  });
  socket = new WebSocket(endpoint);
  await new Promise(resolve => socket.addEventListener('open', resolve, {once: true}));
  let id = 0;
  const pending = new Map(), errors = [];
  const call = (method, params = {}, sessionId) => new Promise((resolve, reject) => { pending.set(++id, {resolve, reject}); socket.send(JSON.stringify({id, method, params, sessionId})); });
  socket.addEventListener('message', event => {
    const m = JSON.parse(event.data);
    if (m.method === 'Runtime.exceptionThrown') errors.push(m.params);
    if (m.method === 'Fetch.requestPaused') call('Fetch.fulfillRequest', {requestId: m.params.requestId, responseCode: 200, responseHeaders: [{name: 'Content-Type', value: 'application/javascript'}], body: readFileSync(asset).toString('base64')}, m.sessionId).catch(e => errors.push(String(e)));
    if (m.id) { const p = pending.get(m.id); pending.delete(m.id); m.error ? p.reject(Error(JSON.stringify(m.error))) : p.resolve(m.result); }
  });
  const {targetId} = await call('Target.createTarget', {url: 'about:blank'});
  const {sessionId} = await call('Target.attachToTarget', {targetId, flatten: true});
  const page = (method, params) => call(method, params, sessionId);
  const evaluate = async expression => { const r = await page('Runtime.evaluate', {expression, returnByValue: true, awaitPromise: true}); if (r.exceptionDetails) throw Error(JSON.stringify(r.exceptionDetails)); return r.result.value; };
  await page('Runtime.enable'); await page('Network.enable');
  for (const mode of (asset ? ['offline', 'nojs', 'pinned'] : ['offline', 'nojs'])) {
    await page('Network.setBlockedURLs', {urls: mode === 'pinned' ? [] : ['http://*', 'https://*']});
    if (mode === 'pinned') await page('Fetch.enable', {patterns: [{urlPattern: 'https://cdn.jsdelivr.net/npm/echarts@5.6.0/dist/echarts.min.js', requestStage: 'Request'}]});
    await page('Emulation.setScriptExecutionDisabled', {value: mode === 'nojs'});
    for (const width of [1440, 390, 320]) {
      await page('Emulation.setDeviceMetricsOverride', {width, height: 900, deviceScaleFactor: 1, mobile: width < 700});
      await page('Page.navigate', {url: pathToFileURL(process.argv[2]).href});
      for (let i = 0; i < 300; i++) {
        if (await evaluate(`document.readyState==='complete' && !!document.querySelector('#db-sample-fallback .db-sample-experiment')`)) break;
        await new Promise(r => setTimeout(r, 50));
      }
      assert.equal(await evaluate(`document.querySelectorAll('#db-sample-fallback .db-sample-experiment').length`), 8);
      assert.equal(await evaluate('document.documentElement.scrollWidth>innerWidth'), false, `${mode} overflow at ${width}`);
      if (mode === 'nojs') {
        assert.equal(await evaluate(`document.getElementById('db-sample-fallback').open`), true);
        assert.ok(await evaluate(`document.getElementById('db-sample-fallback').textContent.includes('25,460')`));
        continue;
      }
      assert.equal(await evaluate(`document.querySelectorAll('#db-sample-metric option').length`), 4);
      const results = await evaluate(`(() => {
        const m=document.getElementById('db-sample-metric'),w=document.getElementById('db-sample-window'),results=[];
        for(let i=0;i<m.options.length;i++) {
          m.selectedIndex=i;m.dispatchEvent(new Event('change'));
          if(w.options.length!==2) throw Error('wrong selected windows');
          for(let j=0;j<w.options.length;j++) {
            w.selectedIndex=j;w.dispatchEvent(new Event('change'));
            const text=document.getElementById('db-sample-selection').textContent;
            if(!text.includes('Matched:')) throw Error('unmatched real experiment');
            const labels=document.getElementById('db-sample-label');
            for(let k=0;k<labels.options.length;k++) {
              labels.selectedIndex=k;labels.dispatchEvent(new Event('change'));
              if(!document.getElementById('db-sample-values').textContent.includes(labels.value)) throw Error('missing label distribution');
            }
            results.push({metric:m.options[i].textContent,window:w.options[j].textContent,text});
          }
        }
        return results;
      })()`);
      assert.equal(results.length, 8);
      const velero = results.filter(r => r.metric.includes('kube_job_status_succeeded'));
      assert.ok(velero.every(r => r.metric.includes('namespace="velero"') && r.metric.includes('cluster="int-westus3-mgmt-1"') && r.text.includes('5,092')));
      assert.ok(results.find(r => r.metric.includes('kube_pod_info') && r.window.includes('hcp-peak')).text.includes('4,068'));
      assert.equal(await evaluate('document.documentElement.scrollWidth>innerWidth'), false, `${mode} selected overflow at ${width}`);
      if (mode === 'pinned') assert.equal(await evaluate('echarts.version'), '5.6.0');
    }
  }
  assert.deepEqual(errors, []);
  console.log('Selected sample panel: all 8 pairs, 4 metrics, all labels, desktop/mobile, no-JS/offline/pinned modes passed.');
} finally {
  socket?.close(); chrome.kill();
}
