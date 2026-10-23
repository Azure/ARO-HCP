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

// node sqlite_events_browser_test.mjs /path/to/events-preview.html
import {spawn} from 'node:child_process';
import assert from 'node:assert/strict';
import {pathToFileURL} from 'node:url';

const chrome = spawn(process.env.CHROME || '/usr/bin/google-chrome', ['--headless', '--no-sandbox', '--disable-gpu', '--disable-background-networking', '--no-first-run', '--no-default-browser-check', '--remote-debugging-port=0', 'about:blank']);
let socket;
try {
  const endpoint = await new Promise((resolve, reject) => {
    let stderr = '';
    const timer = setTimeout(() => reject(Error('Chrome startup timeout')), 20000);
    chrome.stderr.on('data', chunk => {
      stderr += chunk;
      const match = stderr.match(/DevTools listening on (ws:\/\/[^\s]+)/);
      if (match) { clearTimeout(timer); resolve(match[1]); }
    });
    chrome.on('error', reject);
  });
  socket = new WebSocket(endpoint);
  await new Promise(resolve => socket.addEventListener('open', resolve, {once:true}));
  let id = 0;
  const pending = new Map(), errors = [];
  socket.addEventListener('message', event => {
    const msg = JSON.parse(event.data);
    if (msg.method === 'Runtime.exceptionThrown') errors.push(msg.params);
    if (msg.id) {
      const p = pending.get(msg.id); pending.delete(msg.id);
      msg.error ? p.reject(Error(JSON.stringify(msg.error))) : p.resolve(msg.result);
    }
  });
  const call = (method, params = {}, sessionId) => new Promise((resolve, reject) => {
    pending.set(++id, {resolve, reject}); socket.send(JSON.stringify({id, method, params, sessionId}));
  });
  const {targetId} = await call('Target.createTarget', {url:'about:blank'});
  const {sessionId} = await call('Target.attachToTarget', {targetId, flatten:true});
  const page = (method, params) => call(method, params, sessionId);
  const evaluate = async expression => {
    const result = await page('Runtime.evaluate', {expression, returnByValue:true, awaitPromise:true});
    if (result.exceptionDetails) throw Error(JSON.stringify(result.exceptionDetails));
    return result.result.value;
  };
  await page('Runtime.enable'); await page('Network.enable');
  await page('Network.setBlockedURLs', {urls:['http://*', 'https://*']});
  for (const nojs of [false, true]) {
    await page('Emulation.setScriptExecutionDisabled', {value:nojs});
    for (const width of [1440, 390, 320]) {
      await page('Emulation.setDeviceMetricsOverride', {width, height:900, deviceScaleFactor:1, mobile:width<700});
      await page('Page.navigate', {url:pathToFileURL(process.argv[2]).href});
      for (let i=0; i<200; i++) {
        if (await evaluate(`document.readyState==='complete' && !!document.querySelector('.db-namespace-select')`)) break;
        await new Promise(resolve => setTimeout(resolve, 50));
      }
      assert.equal(await evaluate('document.documentElement.scrollWidth>innerWidth'), false, `overflow ${width} nojs=${nojs}`);
      assert.equal(await evaluate(`document.querySelectorAll('#db-events .plots .card').length`), 2);
      assert.equal(await evaluate(`document.querySelectorAll('#db-events .plots svg').length`), 2);
      assert.equal(await evaluate(`document.querySelectorAll('[data-namespace-workspace]').length`), 402);
      assert.ok(await evaluate(`document.getElementById('db-events').textContent.includes('2026-09-21 04:58:00 UTC')`));
      if (nojs) continue;
      const originalSelection = await evaluate(`document.getElementById('db-selection').textContent`);
      const rates = await evaluate(`document.querySelector('.event-reconciliation').textContent`);
      await evaluate(`document.getElementById('db-namespace-filter').value='velero'; document.getElementById('db-namespace-filter').dispatchEvent(new Event('input'));`);
      const visible = await evaluate(`Array.from(document.querySelectorAll('[data-namespace-workspace]')).filter(r=>!r.hidden && r.querySelector('button')).length`);
      assert.equal(visible, 2);
      await evaluate(`Array.from(document.querySelectorAll('[data-namespace-workspace]')).find(r=>!r.hidden && r.querySelector('button')).querySelector('button').click()`);
      assert.ok(await evaluate(`document.getElementById('db-namespace-selection').textContent.includes('velero')`));
      assert.equal(await evaluate(`document.querySelectorAll('#db-namespace-metrics tr').length`), 21);
      assert.ok(await evaluate(`document.getElementById('db-namespace-metrics').textContent.includes('Other metrics (exact remainder)')`));
      assert.equal(await evaluate(`document.getElementById('db-selection').textContent`), originalSelection, 'namespace drilldown changed existing selected metric');
      assert.equal(await evaluate(`document.querySelector('.event-reconciliation').textContent`), rates, 'filter changed reconciliation');
      assert.equal(await evaluate('document.documentElement.scrollWidth>innerWidth'), false);
    }
  }
  assert.deepEqual(errors, []);
  console.log('Events and namespace report: desktop/390px/320px, CDN blocked, no-JS tables, search and drilldown passed.');
} finally {
  if (socket) socket.close();
  chrome.kill('SIGKILL');
}
